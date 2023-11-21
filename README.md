# Clone of `http.ListenAndServe`
Exercise of syscall and async I/O

## Usage
### Start Server
```bash
$ cd internal/example
$ cat main.go
package main

import (
        "fmt"

        "github.com/arailly/go-http-clone"
)

func main() {
        http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
                w.Write([]byte("Hello World\n"))
        })
        fmt.Println(http.ListenAndServe(":8080", nil))
}

$ go run main.go

```
### Test
```bash
$ curl -i localhost:8080
HTTP/1.1 200
Content-Length: 11

Hello World

```

## Features
1. Use syscall instead of `net` or `net/http` package
2. Asynchronous I/O with `epoll`

## Limitations
- GET only
    - other methods and request body is not available :)
- Headers are not available.
- Although I/O will not be blocked, user-defined handler will be blocked when accessed simultaneously.
