package main

import (
	"io"
	"net/http"
	"strings"

	"k8s.io/klog/v2"
)

type rwsink struct {
	sink klog.LogSink
	w    http.ResponseWriter
}

func (s rwsink) Init(info klog.RuntimeInfo) {
	s.sink.Init(info)
}

func (s rwsink) Enabled(level int) bool {
	return s.sink.Enabled(level)
}

func (s rwsink) Info(level int, msg string, keysAndValues ...any) {
	s.sink.Info(level, msg, keysAndValues...)

	// format message
	// msgf := fmt.Sprintf(msg, keysAndValues...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}

	// and log to response-stream incl flush
	// to make client aware of the progress
	io.WriteString(s.w, msg)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}

}
func (s rwsink) Error(err error, msg string, keysAndValues ...any) {
	s.sink.Error(err, msg, keysAndValues...)
}

func (s rwsink) WithValues(keysAndValues ...any) klog.LogSink {
	return s.sink.WithValues(keysAndValues...)
}

func (s rwsink) WithName(name string) klog.LogSink {
	return s.sink.WithName(name)
}
