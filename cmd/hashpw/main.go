package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"to-do-list/internal/auth/password"
	"to-do-list/internal/config"
)

func main() {
	var plain string

	if len(os.Args) > 1 {
		plain = strings.Join(os.Args[1:], " ")
	} else {
		fmt.Fprint(os.Stderr, "Password to hash: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "read password:", err)
			os.Exit(1)
		}
		plain = strings.TrimRight(line, "\r\n")
	}

	if plain == "" {
		fmt.Fprintln(os.Stderr, "error: empty password")
		os.Exit(1)
	}

	cost, err := config.PasswordHashingCost(config.DefaultConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read password.bcrypt_cost from config.yaml:", err)
		os.Exit(1)
	}

	hasher, err := password.NewHasher(cost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build password hasher:", err)
		os.Exit(1)
	}

	hash, err := hasher.Hash(plain)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}

	fmt.Println(hash)
}
