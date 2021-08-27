package squeeze_chromedp

import (
	"context"
)

type activeConn struct {
	p     *Pool
	pc    *poolConn
}

// Close put the Conn back to the pool
func (ac *activeConn) Close() error {
	pc := ac.pc
	if pc == nil {
		return nil
	}
	ac.pc = nil

	return ac.p.put(pc, pc.c.Err() != nil)
}

func (ac *activeConn) Err() error {
	pc := ac.pc
	if pc == nil {
		return errConnClosed
	}
	return pc.c.Err()
}

func (ac *activeConn) Get() context.Context {
	return ac.pc.c.Get()
}
