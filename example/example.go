package main

import (
	"github.com/vito-go/fsearch"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

func init() {
	// Change current working directory to the directory of this file
	_, file, _, _ := runtime.Caller(0)
	err := os.Chdir(filepath.Dir(file))
	if err != nil {
		panic(err)
	}
}
func main() {
	search, err := fsearch.NewDirSearch("../testdata/")
	if err != nil {
		panic(err)
	}
	const defaultMaxLines = 20
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(time.Second*60*60*24*3), 10))
			return
		}
		query := r.URL.Query()
		kws := query["kw"]
		log.Println("kws is=>", kws)
		maxLines, err := strconv.Atoi(query.Get("maxLines"))
		if err != nil {
			maxLines = defaultMaxLines
		}
		lines := search.SearchFromEnd(maxLines, kws...)

		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
	})
	log.Println("start server at :8081")
	err = http.ListenAndServe(":8081", mux)
	if err != nil {
		panic(err)
	}
	// curl --location --request GET '127.0.0.1:8081/search?kw=criteria&maxLines=5'
	// Output:
	// (vi) To be directly or tangibly associated with events or living traditions, with ideas, or with beliefs, with artistic and literary works of outstanding universal significance. (The Committee considers that this criterion should preferably be used in conjunction with other criteria)
	// For cultural sites, the following six criteria can apply:
}
