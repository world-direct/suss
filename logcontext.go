package main

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

var (
	logWriters []http.ResponseWriter
	mu         sync.Mutex
)

func registerLogWriter(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()

	logWriters = append(logWriters, w)
}

func unregisterLogWriter(w http.ResponseWriter) {
	mu.Lock()
	defer mu.Unlock()

	for i := range logWriters {
		if logWriters[i] == w {
			logWriters = append(logWriters[:i], logWriters[i+1:]...)
		}
	}

}

type rwsink struct {
	sink klog.LogSink
}

func (s rwsink) Init(info klog.RuntimeInfo) {
	s.sink.Init(info)
}

func (s rwsink) Enabled(level int) bool {
	return s.sink.Enabled(level)
}

func (s rwsink) Info(level int, msg string, keysAndValues ...any) {
	s.sink.Info(level, msg, keysAndValues...)

	if len(logWriters) == 0 {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}

	// write to each writer
	for i := range logWriters {
		w := logWriters[i]
		io.WriteString(w, msg)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
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
