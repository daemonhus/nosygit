package logging

import "fmt"

var verbose bool = false

func Verbosely(msg string) {
	if verbose {
		fmt.Println(msg)
	}
}
