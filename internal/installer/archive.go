package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const maxArchiveEntries = 1000

// extractBinary returns the content of the single expected executable. Only
// that file is ever written to disk, so archive paths never reach the
// filesystem; suspicious entries still abort the install as a tamper signal.
func extractBinary(data []byte, isZip bool, want string, maxBytes int64) ([]byte, error) {
	if isZip {
		return extractZip(data, want, maxBytes)
	}
	return extractTarGz(data, want, maxBytes)
}

func checkEntryName(name string) (string, error) {
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("entrada com barra invertida no pacote: %q", name)
	}
	clean := path.Clean(name)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("entrada fora do pacote: %q", name)
	}
	return clean, nil
}

func extractTarGz(data []byte, want string, maxBytes int64) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pacote tar.gz inválido: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var found []byte
	for entries := 0; ; entries++ {
		if entries > maxArchiveEntries {
			return nil, errors.New("pacote com entradas demais")
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pacote tar.gz inválido: %w", err)
		}
		name, err := checkEntryName(hdr.Name)
		if err != nil {
			return nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("tipo de entrada não permitido no pacote: %q", hdr.Name)
		}
		if name != want {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%s aparece mais de uma vez no pacote", want)
		}
		if found, err = readLimited(tr, maxBytes); err != nil {
			return nil, err
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%s não encontrado no pacote", want)
	}
	return found, nil
}

func extractZip(data []byte, want string, maxBytes int64) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("pacote zip inválido: %w", err)
	}
	if len(zr.File) > maxArchiveEntries {
		return nil, errors.New("pacote com entradas demais")
	}
	var found []byte
	for _, f := range zr.File {
		name, err := checkEntryName(f.Name)
		if err != nil {
			return nil, err
		}
		mode := f.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("tipo de entrada não permitido no pacote: %q", f.Name)
		}
		if name != want {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%s aparece mais de uma vez no pacote", want)
		}
		if f.UncompressedSize64 > uint64(maxBytes) {
			return nil, fmt.Errorf("%s excede o limite de tamanho", want)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		found, err = readLimited(rc, maxBytes)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%s não encontrado no pacote", want)
	}
	return found, nil
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("binário excede o limite de tamanho")
	}
	return data, nil
}
