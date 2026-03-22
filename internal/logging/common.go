package logging

import (
	"fmt"
	"os"
)

var verbose bool = false

func Verbosely(msg string) {
	if verbose {
		fmt.Fprintln(os.Stderr, msg)
	}
}
