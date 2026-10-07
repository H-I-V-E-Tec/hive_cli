// Package cli implements the hive launcher commands and the dispatch of every
// other command to the installed products.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/mod/semver"

	"github.com/H-I-V-E-Tec/hive_cli/internal/auth"
	"github.com/H-I-V-E-Tec/hive_cli/internal/dispatch"
	"github.com/H-I-V-E-Tec/hive_cli/internal/installer"
	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
	"github.com/H-I-V-E-Tec/hive_cli/internal/release"
	"github.com/H-I-V-E-Tec/hive_cli/internal/store"
	"github.com/H-I-V-E-Tec/hive_cli/internal/verify"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	// latestFreshFor bounds how long a cached "latest" is shown by `version`.
	latestFreshFor = 24 * time.Hour
	// compatProduct receives commands the launcher does not own (setup,
	// doctor, search...), keeping existing docs and scripts working.
	compatProduct = "mind"
)

type App struct {
	Version      string
	Store        *store.Store
	NewInstaller func() (*installer.Installer, error)
	Dispatch     func(binary string, args, env []string) (int, error)
	Launcher     string
	Environ      []string
	GOOS         string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}

func Main(version string, args []string) int {
	st, err := store.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive:", err)
		return exitError
	}
	launcher, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(launcher); resolveErr == nil {
			launcher = resolved
		}
	}
	app := &App{
		Version:  version,
		Store:    st,
		Dispatch: dispatch.Run,
		Launcher: launcher,
		Environ:  os.Environ(),
		GOOS:     runtime.GOOS,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
	app.NewInstaller = func() (*installer.Installer, error) {
		if err := st.Ensure(); err != nil {
			return nil, err
		}
		sig, err := verify.NewPublicGoodSigstore(st.SigstoreCache())
		if err != nil {
			return nil, err
		}
		return &installer.Installer{
			Store:      st,
			Releases:   release.New("hive-cli/" + version),
			Signatures: sig,
			Smoke:      installer.RunVersion,
			GOOS:       runtime.GOOS,
			GOARCH:     runtime.GOARCH,
			Log:        os.Stderr,
		}, nil
	}
	return app.Run(args)
}

func (a *App) Run(args []string) int {
	if len(args) == 0 {
		a.help(a.Stdout)
		return exitOK
	}
	switch args[0] {
	case "help", "-h", "--help":
		a.help(a.Stdout)
		return exitOK
	case "install":
		return a.cmdInstall(args[1:])
	case "update":
		return a.cmdUpdate(args[1:])
	case "rollback":
		return a.cmdRollback(args[1:])
	case "uninstall":
		return a.cmdUninstall(args[1:])
	case "version", "--version":
		return a.cmdVersion(args[1:])
	case "login":
		return a.cmdLogin(args[1:])
	case "logout":
		return a.cmdLogout(args[1:])
	case "setup", "doctor":
		if len(args) > 1 && args[1] == "atlas" {
			p, _ := registry.Lookup("atlas")
			return a.run(p, append([]string{args[0]}, args[2:]...))
		}
	case "list":
		return a.cmdList(args[1:])
	}
	if p, ok := registry.Lookup(args[0]); ok {
		if len(args) > 1 && args[1] == "login" {
			return a.cmdLogin(args[2:])
		}
		if len(args) > 1 && args[1] == "logout" {
			return a.cmdLogout(args[2:])
		}
		return a.run(p, args[1:])
	}
	compat, _ := registry.Lookup(compatProduct)
	return a.run(compat, args)
}

func (a *App) help(w io.Writer) {
	fmt.Fprintf(w, `hive %s — launcher dos produtos HIVE

Produtos:
  hive install <produto> [--version vX.Y.Z] [--allow-downgrade]
  hive update [<produto>...] [--check]   sem produto: atualiza todos e o próprio hive
  hive rollback <produto>                volta para a versão anterior mantida
  hive uninstall <produto>
  hive version [--json]                  versões do hive e dos produtos instalados
  hive list                              produtos disponíveis

Executar um produto:
  hive <produto> [args...]               ex.: hive mind search <programa> <consulta>
  hive atlas                            inicia o MCP Atlas (Go; releases Python antigas continuam suportadas)

Sessão única:
  hive login [--center-url URL] [--check] JWT compartilhado com permissões do usuário
  hive logout                           remove a sessão local de todos os produtos

Outros comandos (setup, doctor, search...) são repassados ao Hive Mind.
`, a.Version)
}

func (a *App) state() (*store.State, error) {
	return a.Store.Load()
}

func (a *App) binaryPath(p registry.Product, version string) string {
	p = registry.InstalledProduct(p, a.Store.VersionDir(p.Name, version), a.GOOS)
	return filepath.Join(a.Store.VersionDir(p.Name, version), p.InstalledName(a.GOOS))
}

func (a *App) run(p registry.Product, args []string) int {
	st, err := a.state()
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive:", err)
		return exitError
	}
	ps, ok := st.Installed(p.Name)
	if !ok {
		fmt.Fprintf(a.Stderr, "hive: %s não está instalado; rode: hive install %s\n", p.Name, p.Name)
		return exitUsage
	}
	p = registry.InstalledProduct(p, a.Store.VersionDir(p.Name, ps.Active), a.GOOS)
	command, productArgs, err := dispatch.ProductCommand(p, a.binaryPath(p, ps.Active), args, a.GOOS)
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive:", err)
		return exitError
	}
	code, err := a.Dispatch(command, productArgs, dispatch.Env(a.Environ, a.Launcher))
	if err != nil {
		fmt.Fprintf(a.Stderr, "hive: não foi possível executar %s: %v\n", p.Name, err)
		return exitError
	}
	return code
}

func lookupProduct(name string) (registry.Product, error) {
	p, ok := registry.Lookup(name)
	if !ok {
		var names []string
		for _, p := range registry.All() {
			names = append(names, p.Name)
		}
		return registry.Product{}, fmt.Errorf("produto desconhecido %q; disponíveis: %s", name, strings.Join(names, ", "))
	}
	return p, nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func (a *App) cmdInstall(args []string) int {
	var name, version string
	allowDowngrade := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--allow-downgrade":
			allowDowngrade = true
		case arg == "--version" && i+1 < len(args):
			version = args[i+1]
			i++
		case strings.HasPrefix(arg, "--version="):
			version = strings.TrimPrefix(arg, "--version=")
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(a.Stderr, "hive install: opção desconhecida %q\n", arg)
			return exitUsage
		case name == "":
			name = arg
		default:
			fmt.Fprintln(a.Stderr, "hive install: instale um produto por vez")
			return exitUsage
		}
	}
	if name == "" {
		fmt.Fprintln(a.Stderr, "uso: hive install <produto> [--version vX.Y.Z]")
		return exitUsage
	}
	p, err := lookupProduct(name)
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive install:", err)
		return exitUsage
	}
	in, err := a.NewInstaller()
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive install:", err)
		return exitError
	}
	ctx, cancel := signalContext()
	defer cancel()
	res, err := in.Install(ctx, p, version, allowDowngrade)
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive install:", err)
		return exitError
	}
	if !res.Changed {
		fmt.Fprintf(a.Stdout, "%s %s já está instalado e ativo\n", p.Name, res.Version)
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "✓ %s %s instalado e ativo\n", p.Name, res.Version)
	return exitOK
}

func (a *App) cmdUpdate(args []string) int {
	check := false
	var names []string
	for _, arg := range args {
		switch {
		case arg == "--check":
			check = true
		case arg == "--all":
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(a.Stderr, "hive update: opção desconhecida %q\n", arg)
			return exitUsage
		default:
			names = append(names, arg)
		}
	}
	st, err := a.state()
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive update:", err)
		return exitError
	}
	var targets []registry.Product
	includeSelf := len(names) == 0
	if len(names) == 0 {
		for _, p := range registry.All() {
			if _, ok := st.Installed(p.Name); ok {
				targets = append(targets, p)
			}
		}
	}
	for _, n := range names {
		if n == registry.Launcher.Name {
			includeSelf = true
			continue
		}
		p, err := lookupProduct(n)
		if err != nil {
			fmt.Fprintln(a.Stderr, "hive update:", err)
			return exitUsage
		}
		if _, ok := st.Installed(p.Name); !ok {
			fmt.Fprintf(a.Stderr, "hive update: %s não está instalado; rode: hive install %s\n", p.Name, p.Name)
			return exitUsage
		}
		targets = append(targets, p)
	}

	in, err := a.NewInstaller()
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive update:", err)
		return exitError
	}
	ctx, cancel := signalContext()
	defer cancel()

	failed := false
	for _, p := range targets {
		ps, _ := st.Installed(p.Name)
		latest, err := in.Latest(ctx, p)
		if err != nil {
			fmt.Fprintf(a.Stderr, "✗ %s: %v\n", p.Name, err)
			failed = true
			continue
		}
		if semver.Compare(latest, ps.Active) <= 0 {
			fmt.Fprintf(a.Stdout, "%s %s: atualizado\n", p.Name, ps.Active)
			continue
		}
		if check {
			fmt.Fprintf(a.Stdout, "%s %s: disponível %s\n", p.Name, ps.Active, latest)
			continue
		}
		res, err := in.Install(ctx, p, latest, false)
		if err != nil {
			fmt.Fprintf(a.Stderr, "✗ %s: %v (a versão %s continua ativa)\n", p.Name, err, ps.Active)
			failed = true
			continue
		}
		fmt.Fprintf(a.Stdout, "✓ %s %s → %s (rollback: hive rollback %s)\n", p.Name, res.Previous, res.Version, p.Name)
	}
	if includeSelf {
		if err := a.selfUpdate(ctx, in, check); err != nil {
			fmt.Fprintf(a.Stderr, "✗ hive: %v\n", err)
			failed = true
		}
	}
	if failed {
		return exitError
	}
	return exitOK
}

func (a *App) cmdRollback(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Stderr, "uso: hive rollback <produto>")
		return exitUsage
	}
	p, err := lookupProduct(args[0])
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive rollback:", err)
		return exitUsage
	}
	// Offline by design: the previous version was verified when installed.
	in := &installer.Installer{Store: a.Store, Smoke: installer.RunVersion, GOOS: a.GOOS}
	ctx, cancel := signalContext()
	defer cancel()
	res, err := in.Rollback(ctx, p)
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive rollback:", err)
		return exitError
	}
	fmt.Fprintf(a.Stdout, "✓ %s %s → %s\n", p.Name, res.Previous, res.Version)
	return exitOK
}

func (a *App) cmdUninstall(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Stderr, "uso: hive uninstall <produto>")
		return exitUsage
	}
	p, err := lookupProduct(args[0])
	if err != nil {
		fmt.Fprintln(a.Stderr, "hive uninstall:", err)
		return exitUsage
	}
	in := &installer.Installer{Store: a.Store, GOOS: a.GOOS}
	if err := in.Uninstall(p); err != nil {
		fmt.Fprintln(a.Stderr, "hive uninstall:", err)
		return exitError
	}
	fmt.Fprintf(a.Stdout, "✓ %s removido (login e configurações em %s foram mantidos)\n", p.Name, a.Store.Root)
	return exitOK
}

type versionReport struct {
	Version  string                    `json:"version"`
	Products map[string]productVersion `json:"products"`
}

type productVersion struct {
	Active   string   `json:"active,omitempty"`
	Previous []string `json:"previous,omitempty"`
	Latest   string   `json:"latest,omitempty"`
}

func (a *App) cmdVersion(args []string) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 1 || (len(args) == 1 && !asJSON) {
		fmt.Fprintln(a.Stderr, "uso: hive version [--json]")
		return exitUsage
	}
	report := versionReport{Version: a.Version, Products: map[string]productVersion{}}
	st, err := a.state()
	if err != nil && !asJSON {
		fmt.Fprintln(a.Stderr, "hive:", err)
	}
	if st == nil {
		st = &store.State{}
	}
	for _, p := range registry.All() {
		pv := productVersion{}
		if ps, ok := st.Installed(p.Name); ok {
			pv.Active, pv.Previous = ps.Active, ps.Previous
			if ps.Latest != "" && time.Since(ps.CheckedAt) < latestFreshFor {
				pv.Latest = ps.Latest
			}
		}
		report.Products[p.Name] = pv
	}
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return exitError
		}
		return exitOK
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "hive\t%s\t\n", a.Version)
	for _, p := range registry.All() {
		pv := report.Products[p.Name]
		if pv.Active == "" {
			fmt.Fprintf(tw, "%s\tnão instalado\t(hive install %s)\n", p.Name, p.Name)
			continue
		}
		var notes []string
		if len(pv.Previous) > 0 {
			notes = append(notes, "anterior: "+pv.Previous[0])
		}
		if pv.Latest != "" && semver.Compare(pv.Latest, pv.Active) > 0 {
			notes = append(notes, "disponível: "+pv.Latest)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, pv.Active, strings.Join(notes, "   "))
	}
	if err := tw.Flush(); err != nil {
		return exitError
	}
	return exitOK
}

func (a *App) cmdList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.Stderr, "uso: hive list")
		return exitUsage
	}
	st, err := a.state()
	if err != nil {
		st = &store.State{}
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 3, ' ', 0)
	for _, p := range registry.All() {
		status := "não instalado"
		if ps, ok := st.Installed(p.Name); ok {
			status = ps.Active
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, status, p.Description)
	}
	if err := tw.Flush(); err != nil {
		return exitError
	}
	return exitOK
}

// selfUpdate replaces the launcher binary, only when it lives in ~/.hive/bin
// (installs elsewhere belong to whoever put them there).
func (a *App) selfUpdate(ctx context.Context, in *installer.Installer, check bool) error {
	managed := filepath.Join(a.Store.BinDir(), registry.ExecutableName(registry.Launcher.Command, a.GOOS))
	if resolved, err := filepath.EvalSymlinks(managed); err == nil {
		managed = resolved
	}
	if !semver.IsValid(a.Version) {
		fmt.Fprintf(a.Stdout, "hive %s: versão de desenvolvimento, sem atualização automática\n", a.Version)
		return nil
	}
	latest, err := in.Latest(ctx, registry.Launcher)
	if errors.Is(err, release.ErrNotFound) {
		fmt.Fprintln(a.Stdout, "hive: nenhuma release do launcher publicada ainda")
		return nil
	}
	if err != nil {
		return err
	}
	if semver.Compare(latest, a.Version) <= 0 {
		fmt.Fprintf(a.Stdout, "hive %s: atualizado\n", a.Version)
		return nil
	}
	if check {
		fmt.Fprintf(a.Stdout, "hive %s: disponível %s\n", a.Version, latest)
		return nil
	}
	if a.Launcher != managed {
		return fmt.Errorf("hive %s disponível, mas este launcher (%s) não fica em %s; reinstale pelo install.sh", latest, a.Launcher, a.Store.BinDir())
	}
	unlock, err := a.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	data, err := in.FetchVerified(ctx, registry.Launcher, latest)
	if err != nil {
		return err
	}
	staged := managed + ".new"
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return err
	}
	defer os.Remove(staged)
	smokeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reported, err := in.Smoke(smokeCtx, staged, registry.Launcher.SmokeArgs)
	if err != nil {
		return fmt.Errorf("teste do novo launcher falhou: %w", err)
	}
	if reported != latest {
		return fmt.Errorf("novo launcher informa %q, esperado %q", reported, latest)
	}
	if a.GOOS == "windows" {
		old := managed + ".old"
		_ = os.Remove(old)
		if err := os.Rename(managed, old); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, managed); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ hive %s → %s\n", a.Version, latest)
	return nil
}

func (a *App) cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	center := fs.String("center-url", "", "HIVE Center HTTPS URL")
	check := fs.Bool("check", false, "validate the shared session")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "usage: hive login [--center-url URL] [--check]")
		return exitUsage
	}
	in := a.Stdin
	if in == nil {
		in = os.Stdin
	}
	ctx, cancel := signalContext()
	defer cancel()
	if err := auth.Login(ctx, a.Store.Root, *center, *check, in, a.Stderr); err != nil {
		fmt.Fprintln(a.Stderr, "hive:", err)
		return exitError
	}
	return exitOK
}

func (a *App) cmdLogout(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.Stderr, "usage: hive logout")
		return exitUsage
	}
	if err := auth.Logout(a.Store.Root); err != nil {
		fmt.Fprintln(a.Stderr, "hive:", err)
		return exitError
	}
	fmt.Fprintln(a.Stderr, "Shared HIVE session removed.")
	return exitOK
}
