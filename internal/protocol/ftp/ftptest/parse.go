package ftptest

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// lineReader reads one FTP line at a time. A line may end with CRLF or LF.
type lineReader struct {
	r *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReader(r)}
}

func (r *lineReader) ReadLine() (string, error) {
	line, err := r.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseCommand splits one control line into a command and its argument.
// The command is upper case. A NUL in the line is refused.
func parseCommand(line string) (cmd, arg string, err error) {
	if strings.Contains(line, "\x00") {
		return "", "", fmt.Errorf("NUL in command")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", fmt.Errorf("empty command")
	}
	cmd, arg, _ = strings.Cut(line, " ")
	return strings.ToUpper(cmd), strings.TrimSpace(arg), nil
}
