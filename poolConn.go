package squeeze_chromedp

import (
	"context"
	"sync"
	"time"
)

type idleList struct {
	count       int
	front, back *poolConn
}

type poolConn struct {
	c          Conn
	t          time.Time
	created    time.Time
	next, prev *poolConn
}

func (l *idleList) pushFront(pc *poolConn) {
	pc.next = l.front
	pc.prev = nil
	if l.count == 0 {
		l.back = pc
	} else {
		l.front.prev = pc
	}
	l.front = pc
	l.count++
}

func (l *idleList) popFront() {
	pc := l.front
	l.count--
	if l.count == 0 {
		l.front, l.back = nil, nil
	} else {
		pc.next.prev = nil
		l.front = pc.next
	}
	pc.next, pc.prev = nil, nil
}

func (l *idleList) popBack() {
	pc := l.back
	l.count--
	if l.count == 0 {
		l.front, l.back = nil, nil
	} else {
		pc.prev.next = nil
		l.back = pc.prev
	}
	pc.next, pc.prev = nil, nil
}

type conn struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

// Close close the context
func (c *conn) Close() error {
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	return nil
}

// Err returns a non-nil value when the context is not usable.
func (c *conn) Err() error {
	return nil
}

// Get get the context
func (c *conn) Get() context.Context {
	return c.ctx
}
