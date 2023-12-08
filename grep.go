package fsearch

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type DirGrep struct {
	Dir string
}

func NewDirGrep(dir string) *DirGrep {
	return &DirGrep{Dir: dir}
}

// FileNames get all file names in the directory.
func (f *DirGrep) FileNames() []string {
	return f.fileNamesBy(nil)
}

// fileNamesBy get file names by fileMap, if fileMap is empty, all files in the directory are searched.
func (f *DirGrep) fileNamesBy(fileMap map[string]struct{}) []string {
	dirEntries, err := os.ReadDir(f.Dir)
	if err != nil {
		return nil
	}
	var fileNames []string
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		// filter file by file name when start with .
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// filter file if there is no dot in the file name
		if !strings.Contains(entry.Name(), ".") {
			continue
		}
		// filter file if binary file
		if isBinaryFile(filepath.Join(f.Dir, entry.Name())) {
			continue
		}

		if len(fileMap) == 0 {
			fileNames = append(fileNames, entry.Name())
			continue
		}
		if _, ok := fileMap[entry.Name()]; ok {
			fileNames = append(fileNames, entry.Name())
		}
	}
	return fileNames

}

// check if the file is binary file
func isBinaryFile(filePath string) bool {
	cmd := exec.Command("file", filePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false
	}
	return strings.Contains(out.String(), "binary")
}

// SearchAndWriteParam is the parameter of SearchAndWrite.
type SearchAndWriteParam struct {
	Writer   io.Writer           // The Writer is used to write the search results.
	HostName string              // The HostName is used to distinguish the host where the file is located.
	MaxLines int                 // At most MaxLines lines of output are printed for each file searched. The default is defaultMaxLines.
	FileMap  map[string]struct{} // The FileMap is used to filter the files to be searched. If the fileMap is empty, all files in the directory are searched.
	Kws      []string            // The Kws is the keyword to be searched.
}

type fileNameAndLines struct {
	fileName string
	lines    []string
}

func (f *DirGrep) SearchAndWrite(param *SearchAndWriteParam) {
	if param == nil {
		return
	}
	w := param.Writer
	hostName := param.HostName
	maxLines := param.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	fileMap := param.FileMap
	kws := param.Kws
	if len(kws) == 0 {
		return
	}
	fileNames := f.fileNamesBy(fileMap)
	chanLines := make(chan *fileNameAndLines, len(fileNames))
	var wg sync.WaitGroup
	for _, name := range fileNames {
		name := name
		filePath := filepath.Join(f.Dir, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines := grepFromFile(maxLines, filePath, kws...)
			if len(lines) == 0 {
				return
			}
			chanLines <- &fileNameAndLines{
				fileName: name,
				lines:    lines,
			}
		}()
	}
	go func() {
		wg.Wait()
		close(chanLines)
	}()
	for fLines := range chanLines {
		name := fLines.fileName
		lines := fLines.lines
		_, err := w.Write([]byte(fmt.Sprintf("<<<<<< --------------------%s %s -------------------- >>>>>>\n", hostName, name)))
		if err != nil {
			return
		}
		for _, line := range lines {
			_, err := w.Write([]byte(line + "\n"))
			if err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

type linesWriter struct {
	mux       sync.Mutex
	maxLines  int
	lines     []string
	kwsFilter []string
}

func newLinesWriter(kwsFilter []string, maxLines int) *linesWriter {
	return &linesWriter{
		lines:     make([]string, 0, 16),
		maxLines:  maxLines,
		kwsFilter: kwsFilter,
	}
}
func (l *linesWriter) Write(p []byte) (n int, err error) {
	l.mux.Lock()
	defer l.mux.Unlock()
	if len(l.lines) >= l.maxLines {
		return 0, io.EOF
	}
	text := string(bytes.TrimSpace(p))
	if len(l.kwsFilter) == 0 {
		l.lines = append(l.lines, strings.Split(text, "\n")...)
		if len(l.lines) >= l.maxLines {
			l.lines = l.lines[:l.maxLines]
			return 0, io.EOF
		}
		return len(p), nil
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		write := true
		// if any kw not in line, not write
		for _, kw := range l.kwsFilter {
			if !strings.Contains(line, kw) {
				write = false
				break
			}
		}
		if write {
			l.lines = append(l.lines, line)
			if len(l.lines) >= l.maxLines {
				return 0, io.EOF
			}
		}
	}
	return len(p), nil
}

func grepFromFile(maxLines int, filePath string, kws ...string) []string {
	kws = parseDuplicateKws(kws)
	if len(kws) == 0 {
		return nil
	}
	// maxLines * 2 in case the number of lines found by the grep command in the file is not enough maxLines
	cmdArgs := []string{"-m", strconv.Itoa(maxLines * 2), "--color=never", "-a", "--", kws[0], filePath}
	log.Printf("grep %s\n", strings.Join(cmdArgs, " "))
	cmd := exec.Command("grep", cmdArgs...)
	var w = newLinesWriter(kws[1:], maxLines)
	// set linesWriter to cmd.Stdout and cmd.Stderr
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	if err != nil {
		//  err may be io.EOF, we ignore it
		//  signal: broken pipe when exceed maxLines, we ignore it
		//  exit status 1 when no match, we ignore it
		return w.lines
	}
	return w.lines
}

// parseDuplicateKws parse duplicate []string and remain the order
func parseDuplicateKws(kws []string) []string {
	if len(kws) == 0 {
		return nil
	}
	kwsMap := make(map[string]struct{}, len(kws))
	var kwsResult []string
	for _, kw := range kws {
		if kw == "" {
			continue
		}
		if _, ok := kwsMap[kw]; ok {
			continue
		}
		kwsMap[kw] = struct{}{}
		kwsResult = append(kwsResult, kw)
	}
	return kwsResult
}
