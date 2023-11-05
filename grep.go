package fsearch

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type DirGrep struct {
	Dir string
}

func NewDirGrep(dir string) *DirGrep {
	return &DirGrep{Dir: dir}
}

func (f *DirGrep) FileNames() []string {
	return f.fileNamesBy(nil)
}
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

// SearchAndWriteParam is the parameter of SearchAndWrite.
type SearchAndWriteParam struct {
	Writer   io.Writer           // The writer is used to write the search results.
	HostName string              // The hostName is used to distinguish the host where the file is located.
	MaxLines int                 // At most maxLines lines of output are printed for each file searched. The default is defaultMaxLines.
	FileMap  map[string]struct{} // The fileMap is used to filter the files to be searched. If the fileMap is empty, all files in the directory are searched.
	Kws      []string            // The kws is the keyword to be searched.
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
	for _, name := range fileNames {
		filePath := filepath.Join(f.Dir, name)
		lines := grepFromFile(maxLines, filePath, kws...)
		if len(lines) == 0 {
			continue
		}
		_, err := w.Write([]byte(fmt.Sprintf("<<<<<< --------------------%s %s -------------------- >>>>>>\n", hostName, name)))
		if err != nil {
			return
		}
		for _, line := range lines {
			_, err = w.Write([]byte(line + "\n"))
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
	text := string(p)
	if len(l.kwsFilter) == 0 {
		l.lines = append(l.lines, strings.Split(text, "\n")...)
		if len(l.lines) >= l.maxLines {
			l.lines = l.lines[:l.maxLines]
			return 0, io.EOF
		}
		return len(p), nil
	}
	splits := strings.Split(text, "\n")
	for _, split := range splits {
		var write bool
		for _, kw := range l.kwsFilter {
			if strings.Contains(split, kw) {
				write = true
				break
			}
		}
		if write {
			l.lines = append(l.lines, split)
			if len(l.lines) >= l.maxLines {
				return 0, io.EOF
			}
		}
	}
	return len(p), nil
}

func grepFromFile(maxLines int, filePath string, kws ...string) []string {
	if len(kws) == 0 {
		return nil
	}
	// 定义命令和参数
	// maxLines*2是为了防止grep命令在文件中找到的关键字行数不够maxLines
	cmdStr := fmt.Sprintf("grep -m %d --color=never -a '%s' %s", maxLines*2, kws[0], filePath)
	//cmdStr := fmt.Sprintf("grep -m %d -a '%s' %s", maxLines*2, kws[0], filePath)
	cmd := exec.Command("bash", "-c", cmdStr)
	var w = newLinesWriter(kws[1:], maxLines)
	// 设置命令的标准输出和错误输出
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
