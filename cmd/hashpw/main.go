package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"to-do-list/internal/auth/password"
	"to-do-list/internal/config"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr,
			"error: pass the password on standard input, not as an argument\n"+
				"  echo -n 'the password' | go run ./cmd/hashpw\n"+
				"  make gen-admin-hash   (prompts without echoing)")
		os.Exit(2)
	}

	fmt.Fprint(os.Stderr, "Password to hash: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "read password:", err)
		os.Exit(1)
	}

	plain := strings.TrimRight(line, "\r\n")

	if plain == "" {
		fmt.Fprintln(os.Stderr, "error: empty password")
		os.Exit(1)
	}

	cost, err := config.PasswordHashingCost(config.DefaultConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read password.bcrypt_cost from config.yaml:", err)
		os.Exit(1)
	}

	hasher, err := password.NewHasher(cost, 1)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build password hasher:", err)
		os.Exit(1)
	}

	hash, err := hasher.Hash(context.Background(), plain)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}

	fmt.Println(hash)
}
