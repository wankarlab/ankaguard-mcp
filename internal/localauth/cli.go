package localauth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// RunOperatorCommand implements `operator <set-password|rotate-secret>`. args are
// the arguments after "operator". It works on the data directory alone, so it
// needs machine access and no running server.
func RunOperatorCommand(dataDir string, args []string, stdin *os.File, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: operator <set-password|rotate-secret>")
	}
	switch args[0] {
	case "set-password":
		pw, err := ReadPassword(stdin, stderr)
		if err != nil {
			return err
		}
		if err := SetPassword(dataDir, pw, 0); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Operator password set. All operator sessions were invalidated.")
		return nil
	case "rotate-secret":
		if err := RotateSecret(dataDir); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Signing secret rotated. All sessions and agent tokens were invalidated.")
		return nil
	default:
		return fmt.Errorf("unknown operator command %q (use set-password or rotate-secret)", args[0])
	}
}

// ReadPassword reads the new password: on a TTY it prompts twice without echo;
// otherwise it reads one plain line from stdin.
func ReadPassword(in *os.File, prompt io.Writer) (string, error) {
	if in != nil && term.IsTerminal(int(in.Fd())) {
		fmt.Fprint(prompt, "New operator password: ")
		a, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(prompt)
		if err != nil {
			return "", err
		}
		fmt.Fprint(prompt, "Repeat password: ")
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(prompt)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			return "", errors.New("passwords do not match")
		}
		return string(a), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", errors.New("could not read password from stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
