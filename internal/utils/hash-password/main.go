package main

import (
	"fmt"
	"os"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"golang.org/x/term"
)

func main() {
	fmt.Fprint(os.Stderr, "Admin password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unable to read password")
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil || string(password) != string(confirmation) {
		fmt.Fprintln(os.Stderr, "password confirmation failed")
		os.Exit(1)
	}
	hash, err := (auth.Argon2id{}).Hash(string(password))
	for index := range password {
		password[index] = 0
	}
	for index := range confirmation {
		confirmation[index] = 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "password hash generation failed")
		os.Exit(1)
	}
	fmt.Println(hash)
}
