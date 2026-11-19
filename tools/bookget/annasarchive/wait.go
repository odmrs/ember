package annasarchive

import (
	"context"
	"encoding/json"
	"fmt"
)

const downloadStateScript = `(()=>{const a=[...document.querySelectorAll('a[href]')].find(a=>/^(?:📚\s*)?(?:download now|baixar agora)\b/i.test((a.innerText||'').trim())&&a.getClientRects().length&&!a.hasAttribute('disabled')&&a.getAttribute('aria-disabled')!=='true');const body=document.body?.innerText||'';return JSON.stringify({url:a?.href||'',manual:/complete the manual check|could not verify your browser|I'm not a robot|não sou um robô|verificação manual/i.test(body)})})()`

// waitForDownload observes the page; it never interacts with a CAPTCHA or
// changes the site's countdown. A user can finish the check in the open tab.
func waitForDownload(ctx context.Context, observe func(context.Context) (string, error), progress func(string), showWindow func()) (string, error) {
	announced := false
	for {
		raw, err := observe(ctx)
		if err != nil {
			return "", err
		}
		var state struct {
			URL    string `json:"url"`
			Manual bool   `json:"manual"`
		}
		if err = json.Unmarshal([]byte(raw), &state); err != nil {
			return "", fmt.Errorf("browser download state was not recognized")
		}
		if state.Manual && !announced {
			if progress != nil {
				progress("Aguardando sua verificação na janela dedicada do Chrome. Conclua o CAPTCHA manualmente; o download retomará automaticamente.")
			}
			if showWindow != nil {
				showWindow()
			}
			announced = true
		}
		if !state.Manual && state.URL != "" {
			if progress != nil {
				progress("Link de download disponível. Iniciando download e checagens locais.")
			}
			return state.URL, nil
		}
		if err = pause(ctx); err != nil {
			return "", fmt.Errorf("site verification or free-download waiting period expired; retry when you can complete the browser check: %w", err)
		}
	}
}
