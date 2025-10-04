package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Test 1")
	fmt.Println("Test 2")
	fmt.Println("Test 3")
	fmt.Fprintf(os.Stderr, "%s\n", "Test Stderr")
	fmt.Fprintf(os.Stderr, "%s\n", "Test Stdout")

}