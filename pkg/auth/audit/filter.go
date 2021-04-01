package audit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"reflect"

	"github.com/rancher/rancher/pkg/auth/util"
	"github.com/sirupsen/logrus"
)

func NewAuditLogMiddleware(auditWriter *LogWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &auditHandler{
			next:        next,
			auditWriter: auditWriter,
		}
	}
}

type auditHandler struct {
	next        http.Handler
	auditWriter *LogWriter
}

func (h auditHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if h.auditWriter == nil {
		h.next.ServeHTTP(rw, req)
		return
	}

	user := getUserInfo(req)

	context := context.WithValue(req.Context(), userKey, user)
	req = req.WithContext(context)

	auditLog, err := newAuditLog(h.auditWriter, req)
	if err != nil {
		util.ReturnHTTPError(rw, req, 500, err.Error())
		return
	}

	wr := &wrapWriter{ResponseWriter: rw, auditWriter: h.auditWriter, statusCode: http.StatusOK}
	h.next.ServeHTTP(wr, req)

	if err := auditLog.write(user, req.Header, wr.Header(), wr.statusCode, wr.buf.Bytes()); err != nil {
		logrus.Errorf("Failed to write auditlog as err: %v", err)
	}
}

type wrapWriter struct {
	http.ResponseWriter
	auditWriter *LogWriter
	statusCode  int
	buf         bytes.Buffer
}

func (aw *wrapWriter) WriteHeader(statusCode int) {
	aw.ResponseWriter.WriteHeader(statusCode)
	aw.statusCode = statusCode
}

func (aw *wrapWriter) Write(body []byte) (int, error) {
	aw.buf.Write(body)
	return aw.ResponseWriter.Write(body)
}

func (aw *wrapWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := aw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("Upstream ResponseWriter of type %v does not implement http.Hijacker", reflect.TypeOf(aw.ResponseWriter))
}

func (aw *wrapWriter) CloseNotify() <-chan bool {
	if cn, ok := aw.ResponseWriter.(http.CloseNotifier); ok {
		return cn.CloseNotify()
	}
	logrus.Errorf("Upstream ResponseWriter of type %v does not implement http.CloseNotifier", reflect.TypeOf(aw.ResponseWriter))
	return make(<-chan bool)
}

func (aw *wrapWriter) Flush() {
	if f, ok := aw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
		return
	}
	logrus.Errorf("Upstream ResponseWriter of type %v does not implement http.Flusher", reflect.TypeOf(aw.ResponseWriter))
}
