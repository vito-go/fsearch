package fsearch_test

import (
	"github.com/vito-go/fsearch"
	"os"
)

func ExampleNewDirGrepWithFile() {

	search := fsearch.NewDirGrep("./testdata/")
	search.SearchAndWrite(&fsearch.SearchAndWriteParam{
		Writer:   os.Stdout,
		HostName: "",
		MaxLines: 10,
		FileMap:  map[string]struct{}{"log1.txt": {}},
		Kws:      []string{"human"},
	})
	// Output:

}

func ExampleNewDirGrep() {
	search := fsearch.NewDirGrep("./testdata/")
	search.SearchAndWrite(&fsearch.SearchAndWriteParam{
		Writer:   os.Stdout,
		HostName: "",
		MaxLines: 10,
		FileMap:  nil,
		Kws:      []string{"a"},
	})

	// Output:
}
