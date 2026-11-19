package annasarchive

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DevTools talks over private inherited pipes, never an open debugging port.
// Chrome uses a separate private profile; personal tabs/cookies are untouched.
type devtools struct {
	input   io.WriteCloser
	output  io.ReadCloser
	cmd     *exec.Cmd
	replies chan []byte
	done    chan struct{}
	id      int
	mu      sync.Mutex
}

func startBrowser(ctx context.Context) (*devtools, error) {
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chrome); err != nil {
		return nil, fmt.Errorf("Google Chrome is required for browser search; no browser will be installed automatically")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	profile := filepath.Join(dir, "bookget", "browser")
	if err = os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	childInput, parentInput, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	parentOutput, childOutput, err := os.Pipe()
	if err != nil {
		_ = childInput.Close()
		_ = parentInput.Close()
		return nil, err
	}
	args := []string{"--remote-debugging-pipe", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "about:blank"}
	binary := chrome
	if runtime.GOARCH == "arm64" {
		binary = "/usr/bin/arch"
		args = append([]string{"-arm64", chrome}, args...)
	}
	// Context cancellation stops protocol commands; the deferred close then gives
	// Chrome time to save its session instead of killing it into a crash state.
	cmd := exec.Command(binary, args...)
	cmd.ExtraFiles = []*os.File{childInput, childOutput}
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR"), "LANG=" + os.Getenv("LANG")}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = childInput.Close()
		_ = parentInput.Close()
		_ = parentOutput.Close()
		_ = childOutput.Close()
		return nil, fmt.Errorf("could not launch dedicated Chrome")
	}
	_ = childInput.Close()
	_ = childOutput.Close()
	d := &devtools{input: parentInput, output: parentOutput, cmd: cmd, replies: make(chan []byte, 16), done: make(chan struct{})}
	go func() {
		defer close(d.done)
		reader := bufio.NewReaderSize(parentOutput, 64<<10)
		for {
			data, e := reader.ReadBytes(0)
			if e != nil {
				return
			}
			if len(data) > 8<<20 {
				return
			}
			select {
			case d.replies <- data[:len(data)-1]:
			case <-ctx.Done():
				return
			}
		}
	}()
	return d, nil
}
func (d *devtools) call(ctx context.Context, method string, params any, session string) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.id++
	id := d.id
	request := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		request["sessionId"] = session
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err = d.input.Write(append(data, 0)); err != nil {
		return nil, fmt.Errorf("browser connection closed")
	}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("browser command %s timed out or was cancelled: %w", method, ctx.Err())
		case <-d.done:
			return nil, fmt.Errorf("dedicated browser closed")
		case raw := <-d.replies:
			var reply struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &reply) != nil || reply.ID != id {
				continue
			}
			if reply.Error != nil {
				return nil, fmt.Errorf("browser command %s failed (%d)", method, reply.Error.Code)
			}
			return reply.Result, nil
		}
	}
}
func (d *devtools) evaluate(ctx context.Context, session, expression string) (string, error) {
	raw, err := d.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, session)
	if err != nil {
		return "", err
	}
	var reply struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err = json.Unmarshal(raw, &reply); err != nil {
		return "", err
	}
	if len(reply.ExceptionDetails) > 0 {
		return "", fmt.Errorf("browser page evaluation failed")
	}
	return reply.Result.Value, nil
}
func (d *devtools) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = d.call(ctx, "Browser.close", map[string]any{}, "")
	_ = d.input.Close()
	_ = d.output.Close()
	done := make(chan struct{})
	go func() { _ = d.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
		<-done
	}
}
func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(250 * time.Millisecond):
		return nil
	}
}
func (d *devtools) navigate(ctx context.Context, session, address string) error {
	_, err := d.call(ctx, "Page.navigate", map[string]any{"url": address}, session)
	if err != nil {
		return err
	}
	for {
		state, e := d.evaluate(ctx, session, `JSON.stringify({state:document.readyState,href:location.href})`)
		if e != nil {
			return e
		}
		var page struct {
			State string `json:"state"`
			Href  string `json:"href"`
		}
		if json.Unmarshal([]byte(state), &page) == nil && page.Href != "about:blank" && (page.State == "interactive" || page.State == "complete") {
			expected, _ := url.Parse(address)
			actual, _ := url.Parse(page.Href)
			if actual == nil || actual.Scheme != expected.Scheme || actual.Host != expected.Host {
				return fmt.Errorf("browser navigation left the selected source")
			}
			if strings.HasPrefix(actual.Path, strings.TrimSuffix(expected.Path, "/")) {
				return nil
			}
		}
		if e = pause(ctx); e != nil {
			return e
		}
	}
}
func openBrowserSession(ctx context.Context, base, key string, progress func(string)) (*devtools, string, error) {
	if progress != nil {
		progress("Abrindo a janela dedicada do navegador…")
	}
	d, err := startBrowser(ctx)
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if !success {
			d.close()
		}
	}()
	raw, err := d.call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"}, "")
	if err != nil {
		return nil, "", err
	}
	var target struct {
		ID string `json:"targetId"`
	}
	if json.Unmarshal(raw, &target) != nil || target.ID == "" {
		return nil, "", errors.New("browser did not create a search tab")
	}
	raw, err = d.call(ctx, "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, "")
	if err != nil {
		return nil, "", err
	}
	var attached struct {
		Session string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &attached) != nil || attached.Session == "" {
		return nil, "", errors.New("browser did not attach search tab")
	}
	session := attached.Session
	if progress != nil {
		progress("Navegador aberto. Preparando a sessão da conta…")
	}
	if key != "" {
		if err = d.navigate(ctx, session, base+"/account/"); err != nil {
			return nil, "", err
		}
		expected, _ := json.Marshal(base)
		submitted := false
		announced := false
		for {
			actual, e := d.evaluate(ctx, session, `location.origin`)
			if e != nil {
				return nil, "", e
			}
			if actual != base {
				return nil, "", fmt.Errorf("browser account origin changed; refusing to provide the key")
			}
			ready, e := d.evaluate(ctx, session, `document.querySelector('input[name="key"]') ? "login" : /Logout/i.test(document.body?.innerText || "") ? "account" : "waiting"`)
			if e != nil {
				return nil, "", e
			}
			if ready == "waiting" && !announced {
				announced = announceVerification(ctx, d, session, progress)
			}
			if ready == "account" {
				break
			}
			if ready == "login" && !submitted {
				secret, _ := json.Marshal(key)
				expression := `(()=>{if(location.origin!==` + string(expected) + `)return "wrong origin";const field=document.querySelector('input[name="key"]');if(!field?.form)return "missing form";const action=new URL(field.form.action,location.href);if(action.origin!==location.origin)return "wrong form";field.value=` + string(secret) + `;field.form.requestSubmit();return "submitted"})()`
				status, e := d.evaluate(ctx, session, expression)
				if e != nil {
					return nil, "", e
				}
				if status != "submitted" {
					return nil, "", fmt.Errorf("browser login form not recognized")
				}
				submitted = true
			}
			if e = pause(ctx); e != nil {
				return nil, "", fmt.Errorf("complete the site verification in the dedicated Chrome window and retry")
			}
		}
	}
	success = true
	return d, session, nil
}

func browserSearch(ctx context.Context, base, query, language, key string, progress func(string)) ([]byte, error) {
	d, session, err := openBrowserSession(ctx, base, key, progress)
	if err != nil {
		return nil, err
	}
	defer d.close()
	if progress != nil {
		progress("Consultando o catálogo no navegador…")
	}
	address := base + "/search?" + encodeSearch(query, language)
	if err = d.navigate(ctx, session, address); err != nil {
		return nil, err
	}

	announced := false
	for {
		actual, e := d.evaluate(ctx, session, `location.origin`)
		if e != nil {
			return nil, e
		}
		if actual != base {
			return nil, fmt.Errorf("browser search navigated outside the selected source")
		}
		// Only result cards are returned, never account forms, keys or full pages.
		data, e := d.evaluate(ctx, session, `(()=>{const links=[...document.querySelectorAll('a.js-vim-focus[href^="/md5/"]')];if(links.length)return links.map(a=>{let n=a;for(let i=0;i<6&&n.parentElement;i++){if(n.classList.contains('flex')&&n.classList.contains('pt-3'))break;n=n.parentElement}return n.outerHTML}).join("\n");if(/no (?:files|results|matching files) found/i.test(document.body?.innerText||""))return "No files found";return ""})()`)
		if e != nil {
			return nil, e
		}
		if data != "" {
			if len(data) > 8<<20 {
				return nil, fmt.Errorf("browser results exceed inspection limit")
			}
			return []byte(data), nil
		}
		if !announced {
			announced = announceVerification(ctx, d, session, progress)
		}
		if e = pause(ctx); e != nil {
			return nil, fmt.Errorf("browser did not provide results; complete any visible site verification and retry")
		}
	}
}
func encodeSearch(query, language string) string {
	values := url.Values{"q": {query}, "ext": {"epub"}}
	if language != "" {
		values.Set("lang", language)
	}
	return values.Encode()
}

func announceVerification(ctx context.Context, d *devtools, session string, progress func(string)) bool {
	state, err := d.evaluate(ctx, session, `/complete the manual check|could not verify your browser|I'm not a robot/i.test(document.body?.innerText||"") ? "manual" : "waiting"`)
	if err != nil || state != "manual" {
		return false
	}
	if progress != nil {
		progress("Aguardando você concluir a verificação na janela dedicada do navegador…")
	}
	_, _ = d.call(ctx, "Page.bringToFront", map[string]any{}, session)
	return true
}
