package squeeze_chromedp

import "context"

type errorConn struct{ err error }

func (ec errorConn) Close() error         { return nil }
func (ec errorConn) Err() error           { return ec.err }
func (ec errorConn) Get() context.Context { return nil }
