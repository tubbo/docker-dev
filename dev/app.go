package dev

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/tubbo/docker-dev/linebuffer"
	"github.com/tubbo/docker-dev/watch"
	"github.com/vektra/errors"
	"gopkg.in/tomb.v2"
)

// DefaultThreads specifies the amount of threads that will be consumed
// by default
const DefaultThreads = 5

// ErrUnexpectedExit is the error thrown when the app crashes during a
// request
var ErrUnexpectedExit = errors.New("unexpected exit")

// App represents a running `docker-compose` project
type App struct {
	Name    string
	Scheme  string
	Host    string
	Port    int
	Command *exec.Cmd
	Public  bool
	Events  *Events

	lines       linebuffer.LineBuffer
	lastLogLine string

	address string
	dir     string

	t tomb.Tomb

	stdout  io.Reader
	pool    *AppPool
	lastUse time.Time

	lock sync.Mutex

	booting bool

	readyChan chan struct{}
}

func (a *App) eventAdd(name string, args ...interface{}) {
	args = append([]interface{}{"app", a.Name}, args...)

	str := a.Events.Add(name, args...)
	a.lines.Append("#event " + str)
}

// SetAddress configures the URL that the app is running on
func (a *App) SetAddress(scheme, host string, port int) {
	a.Scheme = scheme
	a.Host = host
	a.Port = port

	if a.Port == 0 {
		a.address = host
	} else {
		a.address = fmt.Sprintf("%s:%d", a.Host, a.Port)
	}
}

// Address returns the URL to this app.
func (a *App) Address() string {
	if a.Port == 0 {
		return a.Host
	}

	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

// Kill terminates the process the app is running on
func (a *App) Kill(reason string) error {
	a.eventAdd("killing_app",
		"pid", a.Command.Process.Pid,
		"reason", reason,
	)

	fmt.Printf("! Killing '%s' (%d)\n", a.Name, a.Command.Process.Pid)
	err := a.Command.Process.Signal(syscall.SIGTERM)
	if err != nil {
		a.eventAdd("killing_error",
			"pid", a.Command.Process.Pid,
			"error", err.Error(),
		)
		fmt.Printf("! Error trying to kill %s: %s", a.Name, err)
	}
	return err
}

func (a *App) watch() error {
	c := make(chan error)

	go func() {
		r := bufio.NewReader(a.stdout)

		for {
			line, err := r.ReadString('\n')
			if line != "" {
				a.lines.Append(line)
				a.lastLogLine = line
				fmt.Fprintf(os.Stdout, "%s[%d]: %s", a.Name, a.Command.Process.Pid, line)
			}

			if err != nil {
				c <- err
				return
			}
		}
	}()

	var err error

	reason := "detected interval shutdown"

	select {
	case err = <-c:
		reason = "stdout/stderr closed"
		err = fmt.Errorf("%s:\n\t%s", ErrUnexpectedExit, a.lastLogLine)
	case <-a.t.Dying():
		err = nil
	}

	a.Kill(reason)
	a.Command.Wait()
	a.pool.remove(a)

	if a.Scheme == "httpu" {
		os.Remove(a.Address())
	}

	a.eventAdd("shutdown")

	fmt.Printf("* App '%s' shutdown and cleaned up\n", a.Name)

	return err
}

// Detect when the app is idle.
func (a *App) idleMonitor() error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if a.pool.maybeIdle(a) {
				a.Kill("app is idle")
				return nil
			}
		case <-a.t.Dying():
			return nil
		}
	}
}

func (a *App) restartMonitor() error {
	tmpDir := filepath.Join(a.dir, "tmp")
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		return err
	}

	restart := filepath.Join(tmpDir, "restart.txt")

	f, err := os.Create(restart)
	if err != nil {
		return err
	}
	f.Close()

	return watch.Watch(restart, a.t.Dying(), func() {
		a.Kill("restart.txt touched")
	})
}

// WaitTilReady listens on the App's `readyChan` for the app to come up
// and returns when it can serve requests.
func (a *App) WaitTilReady() error {
	select {
	case <-a.readyChan:
		// double check we aren't also dying
		select {
		case <-a.t.Dying():
			return a.t.Err()
		default:
			a.lastUse = time.Now()
			return nil
		}
	case <-a.t.Dying():
		return a.t.Err()
	}
}

const (
	// Booting is the thread that will boot
	Booting = iota
	// Running are the threads that are running
	Running
	// Dead are any dead threads
	Dead
)

// Status returns whether the app is currently running
func (a *App) Status() int {
	// These are done in order as separate selects because go's
	// select does not execute case's sequentially, it runs bodies
	// after sampling all channels and picking a random body.
	select {
	case <-a.t.Dying():
		return Dead
	default:
		select {
		case <-a.readyChan:
			return Running
		default:
			return Dead
		}
	}
}

// Log outputs log messages to STDOUT
func (a *App) Log() string {
	var buf bytes.Buffer
	a.lines.WriteTo(&buf)
	return buf.String()
}

// fileExists checks if a file exists and is not a directory before we
// try using it to prevent further errors.
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// GeneratePort outputs a random port number in the range of 3001-3999
// that will be passed as $PORT to the `docker-compose` process for a
// given application.
func GeneratePort() int {
	low := 3001
	hi := 3999

	return low + rand.Intn(hi-low)
}

const executionShell = `exec bash -c '
cd %s

if test -e ~/.powconfig; then
	source ~/.powconfig
fi

if test -e .env; then
	source .env
fi

if test -e .envrc; then
	source .envrc
fi

if test -e .powrc; then
	source .powrc
fi

if test -e .powenv; then
	source .powenv
fi

export PORT=%d

exec docker-compose --no-ansi up'
`

// LaunchApp boots the app with docker-compose and proxies requests to
// it
func (pool *AppPool) LaunchApp(name, dir string) (*App, error) {
	tmpDir := filepath.Join(dir, "tmp")
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		return nil, err
	}

	// dotenv := make(map[string]string)
	// envfile, err := ioutil.ReadFile(filepath.Join(dir, ".env"))
	// contents := string(envfile)
	// lines := strings.Split(contents, "\n")
	// if err == nil {
	// 	for _, line := range lines {
	// 		statement := strings.Split(line, "=")

	// 		if len(statement) > 1 {
	// 			key := statement[0]
	// 			value := statement[1]
	// 			fmt.Println(line)
	// 			dotenv[key] = value
	// 		}
	// 	}
	// }

	_, err = os.Stat(filepath.Join(dir, "docker-compose.yml"))

	if os.IsNotExist(err) {
		return nil, err
	}

	port := GeneratePort()
	shell := os.Getenv("SHELL")

	if shell == "" {
		fmt.Printf("! SHELL env var not set, using /bin/bash by default")
		shell = "/bin/bash"
	}

	execution := fmt.Sprintf(executionShell, dir, port)
	cmd := exec.Command(shell, "-l", "-i", "-c", execution)
	cmd.Dir = dir
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	cmd.Stderr = cmd.Stdout

	err = cmd.Start()
	if err != nil {
		return nil, errors.Context(err, "starting app")
	}

	fmt.Printf("! Booting app '%s' on port %d\n", name, port)

	app := &App{
		Name:      name,
		Command:   cmd,
		Events:    pool.Events,
		stdout:    stdout,
		dir:       dir,
		pool:      pool,
		readyChan: make(chan struct{}),
		lastUse:   time.Now(),
		Port:      port,
	}

	app.eventAdd("booting_app", "port", app.Port)

	stat, err := os.Stat(filepath.Join(dir, "public"))
	if err == nil {
		app.Public = stat.IsDir()
	}

	app.SetAddress("http", "127.0.0.1", port)

	app.t.Go(app.watch)
	app.t.Go(app.idleMonitor)
	app.t.Go(app.restartMonitor)

	app.t.Go(func() error {
		// This is a poor substitute for getting an actual readiness signal
		// from puma but it's good enough.

		app.eventAdd("waiting_on_app")

		ticker := time.NewTicker(250 * time.Millisecond)
		ctx := context.Background()
		docker, err := client.NewClientWithOpts(client.FromEnv)

		if err != nil {
			return err
		}
		docker.NegotiateAPIVersion(ctx)

		var id string

		expectedContainerName := fmt.Sprintf("/%s_web_1", app.Name)

		defer ticker.Stop()

		for {
			containers, err := docker.ContainerList(ctx, *&types.ContainerListOptions{})

			if err != nil {
				return err
			}

			for _, container := range containers {
				for _, containerName := range container.Names {
					if containerName == expectedContainerName {
						id = container.ID
					}
				}
			}

			select {
			case <-app.t.Dying():
				app.eventAdd("dying_on_start")
				fmt.Printf("! Detecting app '%s' dying on start\n", name)
				return fmt.Errorf("app died before booting")
			case <-ticker.C:
				if id == "" {
					continue
				}

				container, err := docker.ContainerInspect(ctx, id)

				if err != nil {
					return err
				}

				switch container.State.Health.Status {
				case types.Healthy:
					app.eventAdd("app_ready")
					fmt.Printf("! App '%s' booted\n", name)
					close(app.readyChan)
					return nil
				case types.Unhealthy:
					app.eventAdd("dying_on_start")
					fmt.Printf("! Detecting app '%s' dying on start\n", name)
					return fmt.Errorf("the %s container is unhealthy, check your logs for more details", expectedContainerName)
				}
			}
		}
	})

	return app, nil
}

func (pool *AppPool) readProxy(name, path string) (*App, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	app := &App{
		Name:      name,
		Events:    pool.Events,
		pool:      pool,
		readyChan: make(chan struct{}),
		lastUse:   time.Now(),
	}

	data = bytes.TrimSpace(data)

	port, err := strconv.Atoi(string(data))
	if err == nil {
		app.SetAddress("http", "127.0.0.1", port)
	} else {
		u, err := url.Parse(string(data))
		if err != nil {
			return nil, err
		}

		var (
			sport, host string
			port        int
		)

		host, sport, err = net.SplitHostPort(u.Host)
		if err == nil {
			port, err = strconv.Atoi(sport)
			if err != nil {
				return nil, err
			}
		} else {
			host = u.Host
		}

		app.SetAddress(u.Scheme, host, port)
	}

	app.eventAdd("proxy_created",
		"destination", fmt.Sprintf("%s://%s", app.Scheme, app.Address()))

	fmt.Printf("* Generated proxy connection for '%s' to %s://%s\n",
		name, app.Scheme, app.Address())

	// to satisfy the tomb
	app.t.Go(func() error {
		<-app.t.Dying()
		return nil
	})

	close(app.readyChan)

	return app, nil
}

// AppPool is the collection of Apps that docker-dev controls
type AppPool struct {
	Dir      string
	IdleTime time.Duration
	Debug    bool
	Events   *Events

	AppClosed func(*App)

	lock sync.Mutex
	apps map[string]*App
}

// Check if an app is idle by diffing the last use time with the current
// time. If it's over the idle time allowance for this pool, the app is
// probably idle and should be killed.
func (pool *AppPool) maybeIdle(app *App) bool {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	diff := time.Since(app.lastUse)
	if diff > pool.IdleTime {
		app.eventAdd("idle_app", "last_used", diff.String())
		delete(pool.apps, app.Name)
		return true
	}

	return false
}

// ErrUnknownApp is thrown when an application cannot be found in
// config
var ErrUnknownApp = errors.New("unknown app")

// App looks up an app in the pool or adds it.
func (pool *AppPool) App(name string) (*App, error) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	if pool.apps == nil {
		pool.apps = make(map[string]*App)
	}

	app, ok := pool.apps[name]
	if ok {
		return app, nil
	}

	path := filepath.Join(pool.Dir, name)

	pool.Events.Add("app_lookup", "path", path)

	stat, err := os.Stat(path)
	destPath, _ := os.Readlink(path)

	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}

		// Check there might be a link there but it's not valid
		_, err := os.Lstat(path)
		if err == nil {
			fmt.Printf("! Bad symlink detected '%s'. Destination '%s' doesn't exist\n", path, destPath)
			pool.Events.Add("bad_symlink", "path", path, "dest", destPath)
		}

		// If possible, also try expanding - to / to allow for apps in subdirs
		possible := strings.Replace(name, "-", "/", -1)
		if possible == name {
			return nil, ErrUnknownApp
		}

		path = filepath.Join(pool.Dir, possible)

		pool.Events.Add("app_lookup", "path", path)

		stat, err = os.Stat(path)
		destPath, _ = os.Readlink(path)

		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}

			// Check there might be a link there but it's not valid
			_, err := os.Lstat(path)
			if err == nil {
				fmt.Printf("! Bad symlink detected '%s'. Destination '%s' doesn't exist\n", path, destPath)
				pool.Events.Add("bad_symlink", "path", path, "dest", destPath)
			}

			return nil, ErrUnknownApp
		}
	}

	canonicalName := name
	aliasName := ""

	// Handle multiple symlinks to the same app
	destStat, err := os.Stat(destPath)
	if err == nil {
		destName := destStat.Name()
		if destName != canonicalName {
			canonicalName = destName
			aliasName = name
		}
	}

	app, ok = pool.apps[canonicalName]

	if !ok {
		if stat.IsDir() {
			app, err = pool.LaunchApp(canonicalName, path)
		} else {
			app, err = pool.readProxy(canonicalName, path)
		}
	}

	if err != nil {
		pool.Events.Add("error_starting_app", "app", canonicalName, "error", err.Error())
		return nil, err
	}

	pool.apps[canonicalName] = app

	if aliasName != "" {
		pool.apps[aliasName] = app
	}

	return app, nil
}

func (pool *AppPool) remove(app *App) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	// Find all instance references so aliases are removed too
	for name, candidate := range pool.apps {
		if candidate == app {
			delete(pool.apps, name)
		}
	}

	if pool.AppClosed != nil {
		pool.AppClosed(app)
	}
}

// ForApps runs the callback function for each app that exists
func (pool *AppPool) ForApps(f func(*App)) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	for _, app := range pool.apps {
		f(app)
	}
}

// Purge kills all apps
func (pool *AppPool) Purge() {
	pool.lock.Lock()

	var apps []*App

	for _, app := range pool.apps {
		apps = append(apps, app)
	}

	pool.lock.Unlock()

	for _, app := range apps {
		app.eventAdd("purging_app")
		app.t.Kill(nil)
	}

	for _, app := range apps {
		app.t.Wait()
	}

	pool.Events.Add("apps_purged")
}
