package main

import (
	"fmt"
	"os"

	"to-do-list/internal/config"
)

func main() {
	url, err := config.DatabaseURL(config.DefaultConfigPath, config.DefaultEnvPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	fmt.Println(url)
}
