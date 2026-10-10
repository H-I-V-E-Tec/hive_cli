package auth

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

const DefaultCenterURL = "https://hive-center.com.br"

func tokenPath(root string) string {
	if path := os.Getenv("HIVE_TOKEN_FILE"); path != "" {
		return path
	}
	return filepath.Join(root, "token")
}

func centerURL(root, value string) (string, error) {
	if value == "" {
		value = os.Getenv("HIVE_CENTER_URL")
	}
	if value == "" {
		if data, err := os.ReadFile(filepath.Join(root, "center-url")); err == nil {
			value = strings.TrimSpace(string(data))
		}
	}
	if value == "" {
		value = DefaultCenterURL
	}
	return ValidateURL(value)
}

func Login(ctx context.Context, root, center string, check bool, in io.Reader, out io.Writer) error {
	center, err := centerURL(root, center)
	if err != nil {
		return err
	}
	validator, err := NewAuthorizer(center)
	if err != nil {
		return err
	}
	if check {
		token := strings.TrimSpace(os.Getenv("HIVE_TOKEN"))
		if token == "" {
			data, err := os.ReadFile(tokenPath(root))
			if err != nil {
				return fmt.Errorf("run hive login first")
			}
			token = strings.TrimSpace(string(data))
		}
		claims, err := validator.Validate(ctx, token)
		if err != nil {
			return fmt.Errorf("session invalid or expired; run hive login")
		}
		fmt.Fprintf(out, "Authenticated as %s; permissions: %s\n", claims.Name, strings.Join(claims.Permissions, ", "))
		return nil
	}
	reader := bufio.NewReader(in)
	fmt.Fprint(out, "Username: ")
	username, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("cannot read username")
	}
	username = strings.TrimSpace(username)
	fmt.Fprint(out, "Password: ")
	var password string
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		data, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return fmt.Errorf("cannot read password")
		}
		password = string(data)
	} else {
		password, err = reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("cannot read password")
		}
		password = strings.TrimRight(password, "\r\n")
	}
	if username == "" || password == "" || len(username) > 320 || len(password) > 128 {
		return fmt.Errorf("valid username and password required")
	}
	client, err := HTTPClient("")
	if err != nil {
		return err
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := JSON(ctx, client, "POST", center+"/auth/token", "", map[string]string{"username": username, "password": password, "audience": "hive"}, &result); err != nil {
		return err
	}
	// No product grant is required for login. Services enforce their own grants.
	claims, err := validator.Validate(ctx, result.AccessToken)
	if err != nil {
		return fmt.Errorf("Center returned an invalid shared session")
	}
	if err := privateWrite(filepath.Join(root, "center-url"), []byte(center+"\n")); err != nil {
		return err
	}
	if err := privateWrite(tokenPath(root), []byte(result.AccessToken+"\n")); err != nil {
		return err
	}
	fmt.Fprintf(out, "Authenticated as %s; permissions: %s (expires in %d seconds)\n", claims.Name, strings.Join(claims.Permissions, ", "), result.ExpiresIn)
	return nil
}

func Logout(root string) error {
	err := os.Remove(tokenPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func privateWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
