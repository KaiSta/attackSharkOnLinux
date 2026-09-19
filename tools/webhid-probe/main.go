// Command webhid-probe serves a small local page that tests whether Chromium's
// WebHID can reach the Attack Shark X11 receiver on Linux.
//
// It exists to answer one question with evidence rather than speculation:
// Attack Shark's browser-based configuration tool is a WebHID application, so
// what this page can and cannot do is what that tool can and cannot do.
//
// That question is now settled, and not in the hub's favour: the hub's device
// database lists a different vendor ID, and it binds only to devices exposing
// an output report with report ID 8, while this receiver declares no output
// reports at all. See docs/vendor-hub.md. The page is kept because it is how
// that was established, and because it reproduces the same answer on demand.
//
// It is strictly read-only - it never calls sendReport or sendFeatureReport, so
// it cannot change anything on the mouse.
//
// Any result the page produces is POSTed back here and written to a JSON file,
// so the answer is recorded even though the browser window belongs to the user.
//
//	attackshark-webhid-probe -addr 127.0.0.1:8765 -out bin/webhid-report.json
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed index.html
var indexHTML []byte

// report is what the page sends back.
type report struct {
	Page       string          `json:"page"`
	UserAgent  string          `json:"userAgent"`
	When       time.Time       `json:"when"`
	Secure     bool            `json:"secureContext"`
	HIDPresent bool            `json:"hidApiPresent"`
	Granted    json.RawMessage `json:"grantedDevices,omitempty"`
	Devices    json.RawMessage `json:"connectedDevices,omitempty"`
	Feature    json.RawMessage `json:"featureReads,omitempty"`
	Input      json.RawMessage `json:"inputReports,omitempty"`
	Notes      []string        `json:"notes,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "address to listen on")
	out := flag.String("out", "", "file to write the JSON report to")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("webhid-probe: ")

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(indexHTML); err != nil {
			log.Printf("serving page: %v", err)
		}
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := saveReport(*out, body); err != nil {
			log.Printf("saving report: %v", err)
		}
		summary(body)
		w.WriteHeader(http.StatusNoContent)
	})

	// /links fetches a page server-side and prints its hrefs. It exists because
	// the vendor's download page serves its "Launch Web Driver" links through
	// markup that a plain text reader drops, and the hub URL behind them is the
	// one thing needed to try the vendor's own tool.
	mux.HandleFunc("/links", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("url")
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			http.Error(w, "pass ?url=https://...", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("into") != "" {
			*out = r.URL.Query().Get("into")
		}
		hrefs, err := extractLinks(target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		fmt.Printf("\n--- links on %s ---\n", target)
		for _, href := range hrefs {
			fmt.Println(href)
		}
		fmt.Println("--- end of links ---")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, strings.Join(hrefs, "\n"))
	})

	log.Printf("listening on http://%s/", listener.Addr())
	if *out != "" {
		log.Printf("reports will be written to %s", *out)
	}
	log.Printf("open it in Chromium, click the button, and pick the receiver in the dialog")

	if err := http.Serve(listener, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// linkClient is deliberately generous with time: these are marketing pages on
// slow CDNs.
var linkClient = &http.Client{Timeout: 30 * time.Second}

var hrefPattern = regexp.MustCompile(`(?:href|src)=["']([^"']+)["']`)

// linkKeywords keeps the useful links and drops navigation, cart and account
// noise when listing what a page points at.
var linkKeywords = []string{
	"driver", "hub", "soft", "download", "web", "setup", "manual", "firmware",
	".js", ".mjs", "assets", "static", "chunk",
}

func extractLinks(target string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Some storefronts serve a stripped-down page to unknown clients.
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0 Safari/537.36")

	resp, err := linkClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var links []string
	for _, match := range hrefPattern.FindAllStringSubmatch(string(body), -1) {
		href := strings.TrimSpace(match[1])
		if href == "" || seen[href] {
			continue
		}
		lower := strings.ToLower(href)
		for _, keyword := range linkKeywords {
			if strings.Contains(lower, keyword) {
				seen[href] = true
				links = append(links, href)
				break
			}
		}
	}
	return links, nil
}

func saveReport(path string, body []byte) error {
	if path == "" {
		return nil
	}
	var pretty any
	if err := json.Unmarshal(body, &pretty); err != nil {
		// Keep the raw bytes even if they are not valid JSON.
		return os.WriteFile(path, body, 0o600)
	}
	indented, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(indented, '\n'), 0o600)
}

// summary prints the headline findings, so the answer is readable in the
// terminal without opening the JSON.
func summary(body []byte) {
	var rep report
	if err := json.Unmarshal(body, &rep); err != nil {
		log.Printf("received %d bytes that are not a report: %v", len(body), err)
		return
	}

	fmt.Println()
	fmt.Println("--- WebHID result -------------------------------------------------")
	fmt.Printf("secure context        %v\n", rep.Secure)
	fmt.Printf("navigator.hid present %v\n", rep.HIDPresent)
	if rep.Error != "" {
		fmt.Printf("page error            %s\n", rep.Error)
	}
	for _, note := range rep.Notes {
		fmt.Printf("note                  %s\n", note)
	}

	var devices []struct {
		ProductName string `json:"productName"`
		VendorID    int    `json:"vendorId"`
		ProductID   int    `json:"productId"`
		Opened      bool   `json:"opened"`
		OpenError   string `json:"openError"`
		Collections []struct {
			UsagePage    int `json:"usagePage"`
			Usage        int `json:"usage"`
			InputReports []struct {
				ReportID int `json:"reportId"`
			} `json:"inputReports"`
			OutputReports []struct {
				ReportID int `json:"reportId"`
			} `json:"outputReports"`
			FeatureReport []struct {
				ReportID int `json:"reportId"`
			} `json:"featureReports"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(rep.Devices, &devices); err == nil {
		fmt.Printf("chosen devices        %d\n", len(devices))
		for _, d := range devices {
			fmt.Printf("  %04x:%04x %q opened=%v", d.VendorID, d.ProductID, d.ProductName, d.Opened)
			if d.OpenError != "" {
				fmt.Printf(" open error=%s", d.OpenError)
			}
			fmt.Println()
			for _, c := range d.Collections {
				fmt.Printf("    usage page 0x%02x usage 0x%02x: in=%d out=%d feature=%d reports\n",
					c.UsagePage, c.Usage, len(c.InputReports), len(c.OutputReports), len(c.FeatureReport))
			}
		}
	}

	var reads []struct {
		ReportID int    `json:"reportId"`
		OK       bool   `json:"ok"`
		Length   int    `json:"byteLength"`
		Error    string `json:"error"`
		Bytes    string `json:"bytes"`
	}
	if err := json.Unmarshal(rep.Feature, &reads); err == nil && len(reads) > 0 {
		fmt.Println("feature report reads:")
		for _, r := range reads {
			if r.OK {
				fmt.Printf("  id 0x%02x ok, %d bytes: %s\n", r.ReportID, r.Length, r.Bytes)
			} else {
				fmt.Printf("  id 0x%02x FAILED: %s\n", r.ReportID, r.Error)
			}
		}
	}

	var inputs []struct {
		ReportID int    `json:"reportId"`
		Length   int    `json:"byteLength"`
		Bytes    string `json:"bytes"`
		Seen     int    `json:"count"`
	}
	if err := json.Unmarshal(rep.Input, &inputs); err == nil && len(inputs) > 0 {
		fmt.Println("input reports seen while listening:")
		for _, in := range inputs {
			fmt.Printf("  id 0x%02x len=%d count=%d bytes=%s\n", in.ReportID, in.Length, in.Seen, in.Bytes)
		}
	}
	fmt.Println("-------------------------------------------------------------------")
	fmt.Println()
}
