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
