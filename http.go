package http

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/arailly/go-http-clone/internal/respwriter"
)

const (
	maxConn   = 1024
	batchSize = 4096
	StatusOK  = 200
)

var (
	statusMessageMap = map[int]string{
		StatusOK: "OK",
	}
)

type Header map[string][]string

type ResponseWriter interface {
	Write([]byte) (int, error)
	WriteHeader(statusCode int)
}

// key is fd
var responseWriterMap = sync.Map{}

// TODO: Method, URL, Proto, Header, Body
type Request struct {
	pattern string
	Method  string // only GET available
}

// key is pattern
var handlerMap = make(map[string]func(ResponseWriter, *Request))

func HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	handlerMap[pattern] = handler
}

type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

func watch(epollFD, fd int, events int) error {
	ev := &unix.EpollEvent{
		Events: uint32(events),
		Fd:     int32(fd),
	}
	err := unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, fd, ev)
	if err != nil {
		return err
	}
	return nil
}

func unwatch(epollFD, fd int) error {
	err := unix.EpollCtl(epollFD, unix.EPOLL_CTL_DEL, fd, nil)
	if err != nil {
		return err
	}
	return nil
}

func handleAccept(epollFD, fd int) {
	for {
		sock, _, err := unix.Accept(fd)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) ||
				errors.Is(err, unix.EWOULDBLOCK) {
				return
			}
			fmt.Printf("accept error: %v\n", err)
			return
		}
		err = unix.SetNonblock(sock, true)
		if err != nil {
			fmt.Printf("setnonblock error: %v\n", err)
			return
		}
		// use edge trigger mode for client socket
		watch(
			epollFD, sock,
			unix.EPOLLET|unix.EPOLLIN|unix.EPOLLOUT,
		)
		responseWriterMap.Store(sock, respwriter.NewResponseWriter(sock))
	}
}

func parseHTTPRequest(msg []byte) (*Request, error) {
	headerBytes := bytes.Split(msg, []byte("\r\n"))
	requestLine := bytes.Split(headerBytes[0], []byte(" "))
	if len(requestLine) != 3 {
		return nil, fmt.Errorf("invalid request line")
	}
	return &Request{
		pattern: string(requestLine[1]),
		Method:  string(requestLine[0]),
	}, nil
}

func httpRequestCompleted(msg []byte) bool {
	return bytes.Contains(msg, []byte("\r\n\r\n")) &&
		msg[len(msg)-2] == byte('\r') && msg[len(msg)-1] == byte('\n')
}

func handleShutdownOrClose(epollFD, fd int) error {
	unwatch(epollFD, fd)
	unix.Shutdown(fd, unix.SHUT_RDWR)
	unix.Close(fd)
	responseWriterMap.Delete(fd)
	return nil
}

var rawRequests = sync.Map{}

func handleReadableTrigger(epollFD, fd int) {
	// read until EAGAIN
	for {
		msg := make([]byte, batchSize)
		n, err := unix.Read(fd, msg)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) {
				break
			}
			fmt.Printf("read error: %v\n", err)
			return
		}
		if n == 0 {
			handleShutdownOrClose(epollFD, fd)
			return
		}
		raw, ok := rawRequests.Load(fd)
		if !ok {
			rawRequests.Store(fd, msg[:n])
		} else {
			rawRequests.Store(fd, append(raw.([]byte), msg[:n]...))
		}
	}

	// parse request
	raw, ok := rawRequests.Load(fd)
	if !ok {
		fmt.Println("raw not found")
		return
	}
	allMsg := raw.([]byte)
	if !httpRequestCompleted(allMsg) {
		return
	}
	rawRequests.Delete(fd)
	request, err := parseHTTPRequest(allMsg)
	if err != nil {
		fmt.Printf("parseHTTPRequest error: %v\n", err)
		return
	}

	// execute user-defined handler
	rw, ok := responseWriterMap.Load(fd)
	if !ok {
		fmt.Println("responseWriter not found")
		return
	}
	handler, ok := handlerMap[request.pattern]
	if !ok {
		fmt.Println("handler not found")
		return
	}
	handler(rw.(ResponseWriter), request)
}

func handleWritableTrigger(fd int) {
	elem, ok := responseWriterMap.Load(fd)
	if !ok {
		return // connection already closed
	}
	rw := elem.(*respwriter.ResponseWriter)
	if rw.BufferLen() == 0 {
		return // no data to write
	}
	// flush buffer
	if err := rw.FlushBuffer(); err != nil {
		// fmt.Printf("flushBuffer error: %v\n", err)
		return
	}
}

func ListenAndServe(addr string, handler Handler) error {
	epollFD, err := unix.EpollCreate1(0)
	if err != nil {
		return err
	}

	// setup listen socket
	listenSock, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	portStr := strings.Split(addr, ":")[1]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}
	sockaddr := &unix.SockaddrInet4{
		Port: port,
	}
	if err := unix.SetsockoptInt(
		listenSock,
		unix.SOL_SOCKET,
		unix.SO_REUSEADDR,
		1,
	); err != nil {
		return err
	}
	if err := unix.SetNonblock(listenSock, true); err != nil {
		return err
	}
	if err := unix.Bind(listenSock, sockaddr); err != nil {
		return err
	}
	if err := unix.Listen(listenSock, maxConn); err != nil {
		return err
	}

	// use level trigger mode for listen socket
	watch(epollFD, listenSock, unix.EPOLLET|unix.EPOLLIN)

	// close all sockets when received SIGINT
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		fmt.Println("terminating...")
		unix.Close(epollFD)
		unix.Shutdown(listenSock, unix.SHUT_RDWR)
		unix.Close(listenSock)
		responseWriterMap.Range(func(key, value interface{}) bool {
			fd := key.(int)
			unix.Shutdown(fd, unix.SHUT_RDWR)
			unix.Close(fd)
			return true
		})
		os.Exit(0)
	}()

	for {
		notifiedEvents := make([]unix.EpollEvent, maxConn+1)
		n, err := unix.EpollWait(epollFD, notifiedEvents, -1)
		if err != nil {
			fmt.Printf("epoll_wait error: %v\n", err)
			return err
		}

		// process events
		for i := 0; i < n; i++ {
			events := notifiedEvents[i].Events
			fd := int(notifiedEvents[i].Fd)

			if events&unix.EPOLLIN != 0 {
				if fd == listenSock {
					handleAccept(epollFD, fd)
					continue
				}
				handleReadableTrigger(epollFD, fd)
			}
			if events&unix.EPOLLOUT != 0 {
				handleWritableTrigger(fd)
			}
			// not interested in EPOLLRDHUP
			// c.f. https://ymmt.hatenablog.com/entry/2013/09/05/150116
		}
	}
}
