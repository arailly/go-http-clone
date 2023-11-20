package respwriter

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
)

const (
	statusOK = 200
)

type ResponseWriter struct {
	fd int

	mutex           sync.Mutex
	responseStatus  int
	responseHeaders map[string][]string
	buffer          []byte
}

func NewResponseWriter(fd int) *ResponseWriter {
	return &ResponseWriter{
		fd:              fd,
		responseHeaders: make(map[string][]string),
	}
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.responseStatus = statusCode
}

func (w *ResponseWriter) flushBuffer() (int, error) {
	// write contents of buffer
	n, err := syscall.Write(w.fd, w.buffer)
	if err != nil {
		// return if write is blocked
		// buffer will be flushed when fd is writable
		if errors.Is(err, syscall.EAGAIN) {
			return 0, nil
		}
		return 0, err
	}
	w.buffer = []byte{}
	return n, nil
}

func (w *ResponseWriter) FlushBuffer() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, err := w.flushBuffer()
	return err
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// push status to buffer
	status := w.responseStatus
	if status == 0 {
		status = statusOK
	}
	response := []byte("HTTP/1.1 " + fmt.Sprint(status) + "\r\n")

	// push headers to buffer
	w.responseHeaders["Content-Length"] = []string{fmt.Sprint(len(b))}
	for k, v := range w.responseHeaders {
		for _, vv := range v {
			response = append(response, []byte(k+": "+vv+"\r\n")...)
		}
	}
	response = append(response, []byte("\r\n")...)

	// push body to buffer
	response = append(response, b...)
	response = append(response, []byte("\r\n")...)

	w.buffer = append(w.buffer, response...)
	w.responseStatus = 0
	w.responseHeaders = make(map[string][]string)

	// write contents of buffer
	return w.flushBuffer()
}

func (w *ResponseWriter) BufferLen() int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return len(w.buffer)
}
