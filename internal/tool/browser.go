package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/gal-cli/gal-cli/internal/provider"
)

type browserInstance struct {
	mu      sync.Mutex
	browser *rod.Browser
	page    *rod.Page
}

var globalBrowser = &browserInstance{}

func (b *browserInstance) ensureBrowser() error {
	if b.browser != nil {
		return nil
	}
	l := launcher.New().
		Headless(false).       // disable old headless
		HeadlessNew(true).     // use new headless mode (harder to detect)
		Set("disable-blink-features", "AutomationControlled")
	if os.Getuid() == 0 {
		l = l.NoSandbox(true)
	}
	u, err := l.Launch()
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	b.browser = rod.New().ControlURL(u)
	if err := b.browser.Connect(); err != nil {
		b.browser = nil
		return fmt.Errorf("connect browser: %w", err)
	}
	return nil
}

func (b *browserInstance) ensurePage() (*rod.Page, error) {
	if err := b.ensureBrowser(); err != nil {
		return nil, err
	}
	if b.page == nil {
		p, err := b.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return nil, err
		}
		// Inject stealth scripts to bypass headless detection
		p.EvalOnNewDocument(stealthJS)
		b.page = p
	}
	return b.page, nil
}

// stealthJS patches common headless browser detection vectors.
const stealthJS = `
Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
Object.defineProperty(navigator, 'languages', {get: () => ['zh-CN','zh','en']});
Object.defineProperty(navigator, 'plugins', {get: () => [1,2,3,4,5]});
window.chrome = {runtime: {}};
const originalQuery = window.navigator.permissions.query;
window.navigator.permissions.query = (parameters) => (
  parameters.name === 'notifications' ?
    Promise.resolve({state: Notification.permission}) :
    originalQuery(parameters)
);
`

func (b *browserInstance) close() string {
	if b.page != nil {
		b.page.Close()
		b.page = nil
	}
	if b.browser != nil {
		b.browser.Close()
		b.browser = nil
	}
	return "browser closed"
}

// getElements returns a simplified representation of interactive elements on the page.
func getElements(page *rod.Page, selector string) (string, error) {
	js := `(sel) => {
		const root = sel ? document.querySelector(sel) : document;
		if (!root) return '(no element matches selector)';
		// Phase 1: standard interactive elements
		const tags = ['a','button','input','textarea','select','[role="button"]','[role="tab"]','[role="menuitem"]','[role="link"]','[onclick]','[contenteditable="true"]'];
		const seen = new Set();
		const lines = [];
		let i = 1;
		function add(el) {
			if (seen.has(el)) return;
			seen.add(el);
			if (!el.offsetParent && el.tagName !== 'BODY') return;
			const tag = el.tagName.toLowerCase();
			let id = el.id ? '#'+el.id : '';
			let cls = el.className && typeof el.className === 'string' ? '.'+el.className.trim().split(/\s+/).join('.') : '';
			let desc = tag + id + cls;
			let extra = [];
			if (el.type) extra.push('type='+el.type);
			if (el.placeholder) extra.push('placeholder="'+el.placeholder+'"');
			if (el.name) extra.push('name="'+el.name+'"');
			let text = (el.textContent||'').trim().substring(0,50);
			if (text) extra.push('"'+text+'"');
			if (el.href) extra.push('href="'+el.href+'"');
			lines.push('['+i+'] '+desc+(extra.length?' '+extra.join(' '):''));
			i++;
		}
		root.querySelectorAll(tags.join(',')).forEach(add);
		// Phase 2: div/span with pointer cursor (common in SPAs)
		root.querySelectorAll('div,span').forEach(el => {
			if (seen.has(el)) return;
			if (!el.offsetParent) return;
			const style = window.getComputedStyle(el);
			if (style.cursor === 'pointer' && el.textContent.trim().length < 50 && el.textContent.trim().length > 0) {
				add(el);
			}
		});
		return lines.length ? lines.join('\n') : '(no interactive elements found)';
	}`
	res, err := page.Eval(js, selector)
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

func (r *Registry) registerBrowser() {
	r.Register(provider.ToolDef{
		Name:        "browser",
		Description: "Headless Chromium browser automation via Chrome DevTools Protocol (CDP). Navigate pages, click, fill forms, extract text, screenshot, execute JS. Elements are targeted by CSS selectors. Use for web scraping, testing, login automation on JS-rendered pages. The browser session persists across calls — navigate first, then interact.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string", "description": "Action: navigate, click, fill, select, screenshot, get_text, get_elements, eval, scroll, wait, close"},
				"url":        map[string]any{"type": "string", "description": "URL to navigate to (for navigate)"},
				"selector":   map[string]any{"type": "string", "description": "CSS selector for target element"},
				"value":      map[string]any{"type": "string", "description": "Value to fill or select"},
				"expression": map[string]any{"type": "string", "description": "JavaScript expression to evaluate (for eval)"},
				"path":       map[string]any{"type": "string", "description": "File path for screenshot (default: /tmp/screenshot.png)"},
				"direction":  map[string]any{"type": "string", "description": "Scroll direction: up or down"},
				"timeout":    map[string]any{"type": "integer", "description": "Timeout in seconds (for wait, default 10)"},
			},
			"required": []string{"action"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		action := getStr(args, "action")
		globalBrowser.mu.Lock()
		defer globalBrowser.mu.Unlock()

		if action == "close" {
			return globalBrowser.close(), nil
		}

		page, err := globalBrowser.ensurePage()
		if err != nil {
			return "", err
		}

		switch action {
		case "navigate":
			u := getStr(args, "url")
			if u == "" {
				return "", fmt.Errorf("url is required for navigate")
			}
			if err := page.Navigate(u); err != nil {
				return "", err
			}
			if err := page.WaitLoad(); err != nil {
				return "", err
			}
			// wait a bit for JS rendering
			time.Sleep(500 * time.Millisecond)
			info, _ := page.Info()
			title := ""
			if info != nil {
				title = info.Title
			}
			elements, _ := getElements(page, "")
			return fmt.Sprintf("[Page: %s]\n[Title: %s]\n%s", u, title, elements), nil

		case "click":
			sel := getStr(args, "selector")
			if sel == "" {
				return "", fmt.Errorf("selector is required for click")
			}
			el, err := page.Timeout(10 * time.Second).Element(sel)
			if err != nil {
				return "", fmt.Errorf("element not found: %s", sel)
			}
			if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return "", err
			}
			time.Sleep(500 * time.Millisecond)
			_ = page.WaitLoad()
			info, _ := page.Info()
			currentURL := ""
			if info != nil {
				currentURL = info.URL
			}
			return fmt.Sprintf("clicked %s, current page: %s", sel, currentURL), nil

		case "fill":
			sel := getStr(args, "selector")
			val := getStr(args, "value")
			if sel == "" {
				return "", fmt.Errorf("selector is required for fill")
			}
			el, err := page.Timeout(10 * time.Second).Element(sel)
			if err != nil {
				return "", fmt.Errorf("element not found: %s", sel)
			}
			el.MustSelectAllText().MustInput(val)
			return fmt.Sprintf("filled %s", sel), nil

		case "select":
			sel := getStr(args, "selector")
			val := getStr(args, "value")
			if sel == "" {
				return "", fmt.Errorf("selector is required for select")
			}
			el, err := page.Timeout(10 * time.Second).Element(sel)
			if err != nil {
				return "", fmt.Errorf("element not found: %s", sel)
			}
			el.MustSelect(val)
			return fmt.Sprintf("selected '%s' in %s", val, sel), nil

		case "screenshot":
			p := getStr(args, "path")
			if p == "" {
				p = "/tmp/screenshot.png"
			}
			data, err := page.Screenshot(true, nil)
			if err != nil {
				return "", err
			}
			if err := writeFile(p, data); err != nil {
				return "", err
			}
			return fmt.Sprintf("screenshot saved to %s (%d bytes)", p, len(data)), nil

		case "get_text":
			sel := getStr(args, "selector")
			if sel == "" {
				text, err := page.Eval(`() => document.body.innerText`)
				if err != nil {
					return "", err
				}
				t := text.Value.Str()
				if len(t) > 4096 {
					t = t[:4096] + "\n...(truncated)"
				}
				return t, nil
			}
			el, err := page.Timeout(10 * time.Second).Element(sel)
			if err != nil {
				return "", fmt.Errorf("element not found: %s", sel)
			}
			t, err := el.Text()
			if err != nil {
				return "", err
			}
			if len(t) > 4096 {
				t = t[:4096] + "\n...(truncated)"
			}
			return t, nil

		case "get_elements":
			sel := getStr(args, "selector")
			return getElements(page, sel)

		case "eval":
			expr := getStr(args, "expression")
			if expr == "" {
				return "", fmt.Errorf("expression is required for eval")
			}
			// wrap in function if not already
			if !strings.HasPrefix(strings.TrimSpace(expr), "(") && !strings.HasPrefix(strings.TrimSpace(expr), "function") {
				expr = fmt.Sprintf("() => { return %s }", expr)
			}
			res, err := page.Eval(expr)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%v", res.Value), nil

		case "scroll":
			dir := getStr(args, "direction")
			amount := 500
			if dir == "up" {
				amount = -500
			}
			page.Eval(fmt.Sprintf(`() => window.scrollBy(0, %d)`, amount))
			return fmt.Sprintf("scrolled %s", dir), nil

		case "wait":
			sel := getStr(args, "selector")
			if sel == "" {
				return "", fmt.Errorf("selector is required for wait")
			}
			timeout := toInt(args["timeout"])
			if timeout <= 0 {
				timeout = 10
			}
			_, err := page.Timeout(time.Duration(timeout) * time.Second).Element(sel)
			if err != nil {
				return "", fmt.Errorf("timeout waiting for %s", sel)
			}
			return fmt.Sprintf("element %s found", sel), nil

		default:
			return "", fmt.Errorf("unknown action: %s (available: navigate, click, fill, select, screenshot, get_text, get_elements, eval, scroll, wait, close)", action)
		}
	})
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// CloseBrowser closes the global browser instance. Call on session end.
func CloseBrowser() {
	globalBrowser.mu.Lock()
	defer globalBrowser.mu.Unlock()
	globalBrowser.close()
}
