package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// shutdownTimeout bounds the graceful drain performed by Start when its context
// is cancelled. In-flight requests get this long to finish before the
// listener's connections are closed.
const shutdownTimeout = 30 * time.Second

// Server is the proxy's HTTP listener with production-sane timeouts and
// graceful shutdown.
type Server struct {
	*http.Server

	// ReadBufferSize and WriteBufferSize are the socket buffer sizes applied to
	// accepted TCP connections (32 KiB by default). net/http manages its own
	// header buffers internally and does not expose them, so tuning the socket
	// buffers is the effective knob for request/response batching.
	ReadBufferSize  int
	WriteBufferSize int
}

// NewServer returns a Server listening on addr and serving handler.
func NewServer(addr string, handler http.Handler) *Server {
	return &Server{
		Server: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
	}
}

// Start serves until ctx is cancelled, then gracefully shuts down with a
// 30 second deadline. It returns nil after a clean shutdown; a listener error
// (for example the port already being in use) is returned as-is.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	ln = &sizedListener{Listener: ln, readBuffer: s.ReadBufferSize, writeBuffer: s.WriteBufferSize}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.Server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Shutdown gracefully stops the server, waiting for in-flight requests to
// finish until ctx is done.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.Server.Shutdown(ctx)
}

// sizedListener applies socket buffer sizes to accepted TCP connections.
type sizedListener struct {
	net.Listener
	readBuffer  int
	writeBuffer int
}

// Accept implements net.Listener.
func (l *sizedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		if l.readBuffer > 0 {
			_ = tc.SetReadBuffer(l.readBuffer)
		}
		if l.writeBuffer > 0 {
			_ = tc.SetWriteBuffer(l.writeBuffer)
		}
	}
	return c, nil
}
