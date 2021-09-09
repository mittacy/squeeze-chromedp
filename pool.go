package squeeze_chromedp

import (
	"context"
	"errors"
	"fmt"
	"github.com/chromedp/chromedp"
	"sync"
	"time"
)

var (
	nowFunc = time.Now

	// ErrPoolExhausted is returned from a pool connection method when
	// the maximum number of database connections in the pool has been reached.
	ErrPoolExhausted = errors.New("squeeze: connection pool exhausted")

	errConnClosed = errors.New("squeeze: connection closed")
)

type Pool struct {
	// InitActive number of connections when the pool init.
	InitActive int

	// Maximum number of idle connections in the pool.
	MaxIdle int

	// Maximum number of connections allocated by the pool at a given time.
	// When zero, there is no limit on the number of connections in the pool.
	MaxActive int

	// Close connections after remaining idle for this duration. If the value
	// is zero, then idle connections are not closed. Applications should set
	// the timeout to a value less than the server's timeout.
	IdleTimeout time.Duration

	// If Wait is true and the pool is at the MaxActive limit, then Get() waits
	// for a connection to be returned to the pool before returning.
	Wait bool

	// Close connections older than this duration. If the value is zero, then
	// the pool does not close connections based on age.
	MaxConnLifetime time.Duration

	// ShowWindow set whether the browser is headless. Forbidden set true in the
	// production environment, that will seriously affect performance and may
	// cause unexpected errors.
	ShowWindow bool

	mu           sync.Mutex    // mu protects the following fields
	closed       bool          // set to true when the pool is closed.
	active       int           // the number of open connections in the pool
	initOnce     sync.Once     // the init ch once func
	ch           chan struct{} // limits open connections when p.Wait is true
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	idle         idleList      // idle connections
	waitCount    int64         // total number of connections waited for.
	waitDuration time.Duration // total time waited for new connections.
}

// PoolStats contains pool statistics.
type PoolStats struct {
	// ActiveCount is the number of connections in the pool. The count includes
	// idle connections and connections in use.
	ActiveCount int

	// IdleCount is the number of idle connections in the pool.
	IdleCount int

	// WaitCount is the total number of connections waited for.
	// This value is currently not guaranteed to be 100% accurate.
	WaitCount int64

	// WaitDuration is the total time blocked waiting for a new connection.
	// This value is currently not guaranteed to be 100% accurate.
	WaitDuration time.Duration
}

func NewPool(poolConfig *Pool) *Pool {
	// create the root context
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", !poolConfig.ShowWindow),
		chromedp.Flag("disable-web-security", true),
		chromedp.NoFirstRun,
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.164 Safari/537.36"),
	)
	opts = append(chromedp.DefaultExecAllocatorOptions[:], opts...)
	rootCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	pool := Pool{
		InitActive:  poolConfig.InitActive,
		MaxIdle:     poolConfig.MaxIdle,
		MaxActive:   poolConfig.MaxActive,
		IdleTimeout: poolConfig.IdleTimeout,
		Wait:        poolConfig.Wait,
		rootCtx:     rootCtx,
		rootCancel:  cancel,
	}

	// init the connections where the InitActive greater than 0
	i := poolConfig.InitActive
	for i > 0 {
		pool.active++
		c, err := pool.newConn()
		if err != nil {
			panic(fmt.Sprintf("squeen-chromedp: %s", err))
		}

		conn := activeConn{
			p: &pool,
			pc: &poolConn{
				c:       c,
				created: time.Time{},
			},
		}

		if err := chromedp.Run(conn.Get(), initWindow()); err != nil {
			panic(err)
		}

		if err := conn.Close(); err != nil {
			panic(fmt.Sprintf("squeen-chromedp: %s", err))
		}
		i--
	}

	return &pool
}

// Get gets a connection. The application must close the returned connection.
// This method always returns a valid connection so that applications can defer
// error handling to the first use of the connection. If there is an error
// getting an underlying connection, then the connection methods return that error.
func (p *Pool) Get() Conn {
	// GetContext returns errorConn in the first argument when an error occurs.
	c, _ := p.GetContext(context.Background())
	return c
}

// Get gets a connection using the provided context.
//
// The provided Context must be non-nil. If the context expires before the
// connection is complete, an error is returned. Any expiration on the context
// will not affect the returned connection.
//
// If the function completes without error, then the application must close the
// returned connection.
func (p *Pool) GetContext(ctx context.Context) (Conn, error) {
	// Wait until there is a vacant connection in the pool.
	waited, err := p.waitVacantConn(ctx)
	if err != nil {
		return errorConn{err}, err
	}

	p.mu.Lock()

	if waited > 0 {
		p.waitCount++
		p.waitDuration += waited
	}

	// Prune stale connections at the back of the idle list.
	if p.IdleTimeout > 0 {
		n := p.idle.count
		for i := 0; i < n && p.idle.back != nil && p.idle.back.t.Add(p.IdleTimeout).Before(nowFunc()); i++ {
			pc := p.idle.back
			p.idle.popBack()
			p.mu.Unlock()
			pc.c.Close()
			p.mu.Lock()
			p.active--
		}
	}

	// Get idle connection from the front of idle list.
	for p.idle.front != nil {
		pc := p.idle.front
		p.idle.popFront()
		p.mu.Unlock()
		if p.TestOnBorrow(pc.c) == nil &&
			(p.MaxConnLifetime == 0 || nowFunc().Sub(pc.created) < p.MaxConnLifetime) {
			return &activeConn{p: p, pc: pc}, nil
		}

		pc.c.Close()
		p.mu.Lock()
		p.active--
	}

	// Check for pool closed before dialing a new connection.
	if p.closed {
		p.mu.Unlock()
		err := errors.New("squeeze: get on closed pool")
		return errorConn{err}, err
	}

	// Handle limit for p.Wait == false.
	if !p.Wait && p.MaxActive > 0 && p.active >= p.MaxActive {
		p.mu.Unlock()
		return errorConn{ErrPoolExhausted}, ErrPoolExhausted
	}

	p.active++
	p.mu.Unlock()

	c, err := p.newConn()
	if err != nil {
		p.mu.Lock()
		p.active--
		if p.ch != nil && !p.closed {
			p.ch <- struct{}{}
		}
		p.mu.Unlock()
		return errorConn{err}, err
	}

	return &activeConn{p: p, pc: &poolConn{c: c, created: nowFunc()}}, nil
}

// Stats returns pool's statistics.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	stats := PoolStats{
		ActiveCount:  p.active,
		IdleCount:    p.idle.count,
		WaitCount:    p.waitCount,
		WaitDuration: p.waitDuration,
	}
	p.mu.Unlock()

	return stats
}

// ActiveCount returns the number of connections in the pool. The count
// includes idle connections and connections in use.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	return active
}

// IdleCount returns the number of idle connections in the pool.
func (p *Pool) IdleCount() int {
	p.mu.Lock()
	idle := p.idle.count
	p.mu.Unlock()
	return idle
}

// Close releases the resources used by the pool.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.active -= p.idle.count
	pc := p.idle.front
	p.idle.count = 0
	p.idle.front, p.idle.back = nil, nil
	if p.ch != nil {
		close(p.ch)
	}
	p.mu.Unlock()
	for ; pc != nil; pc = pc.next {
		pc.c.Close()
	}

	p.rootCancel()
	return nil
}

// TestOnBorrow is an function for checking the health of an idle connection
// before the connection is used again by the application. If the function
// returns an error, then the connection is closed.
func (p *Pool) TestOnBorrow(c Conn) error {
	if err := chromedp.Run(c.Get(), chromedp.Tasks{chromedp.ResetViewport()}); err != nil {
		return err
	}
	return nil
}

// waitVacantConn waits for a vacant connection in pool if waiting
// is enabled and pool size is limited, otherwise returns instantly.
// If ctx expires before that, an error is returned.
//
// If there were no vacant connection in the pool right away it returns the time spent waiting
// for that connection to appear in the pool.
func (p *Pool) waitVacantConn(ctx context.Context) (waited time.Duration, err error) {
	if !p.Wait || p.MaxActive <= 0 {
		// No wait or no connection limit.
		return 0, nil
	}

	p.lazyInit()

	// wait indicates if we believe it will block so its not 100% accurate
	// however for stats it should be good enough.
	wait := len(p.ch) == 0
	var start time.Time
	if wait {
		start = time.Now()
	}

	select {
	case <-p.ch:
		// Additionally check that context hasn't expired while we were waiting,
		// because `select` picks a random `case` if several of them are "ready".
		select {
		case <-ctx.Done():
			p.ch <- struct{}{}
			return 0, ctx.Err()
		default:
		}
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	if wait {
		return time.Since(start), nil
	}
	return 0, nil
}

func (p *Pool) lazyInit() {
	p.initOnce.Do(func() {
		p.ch = make(chan struct{}, p.MaxActive)
		if p.closed {
			close(p.ch)
		} else {
			for i := 0; i < p.MaxActive; i++ {
				p.ch <- struct{}{}
			}
		}
	})
}

func (p *Pool) put(pc *poolConn, forceClose bool) error {
	p.mu.Lock()
	if !p.closed && !forceClose {
		pc.t = nowFunc()
		p.idle.pushFront(pc)
		if p.idle.count > p.MaxIdle {
			pc = p.idle.back
			p.idle.popBack()
		} else {
			pc = nil
		}
	}

	if pc != nil {
		p.mu.Unlock()
		pc.c.Close()
		p.mu.Lock()
		p.active--
	}

	if p.ch != nil && !p.closed {
		p.ch <- struct{}{}
	}
	p.mu.Unlock()
	return nil
}

func (p *Pool) newConn() (Conn, error) {
	ctx, cancel := chromedp.NewContext(p.rootCtx)

	if ctx == nil {
		return nil, errors.New("squeeze: create new chrome window fail")
	}

	c := conn{
		mu:     sync.Mutex{},
		ctx:    ctx,
		cancel: cancel,
	}

	return &c, nil
}
