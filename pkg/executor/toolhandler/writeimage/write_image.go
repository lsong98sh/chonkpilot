package writeimage

import (
	"bytes"
	"compress/flate"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/goccy/go-graphviz"

	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

//go:embed mermaid.min.js
var mermaidJS string

// svgTemplate wraps raw SVG markup in a minimal HTML page.
// No CSS affecting SVGs — any styling on <svg> can collapse height to 0.
const svgTemplate = `<!DOCTYPE html>
<html><body style="margin:0;background:white;font-family:'Microsoft YaHei','PingFang SC','SimHei',sans-serif;">%s</body></html>`

// mermaidTemplate builds a self-contained HTML page that renders Mermaid diagrams.
// Uses mermaid.render() (sync-style Promise API) for reliable results,
// and JSON-encodes the diagram text for safe JS embedding.
func mermaidTemplate(content string) string {
	jsContent, _ := json.Marshal(content)
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body style="margin:0;background:white;font-family:'Microsoft YaHei','PingFang SC','SimHei',sans-serif;">
<div id="o"></div>
<script>%s</script>
<script>
mermaid.initialize({startOnLoad:false,securityLevel:'loose',theme:'default',fontFamily:'"Microsoft YaHei","PingFang SC","SimHei",sans-serif'});
	mermaid.render('s', %s).then(function(r){
	document.getElementById('o').innerHTML = r.svg;
});
</script>
</body></html>`, mermaidJS, string(jsContent))
}

// HandleWriteImage renders SVG, HTML, or Mermaid content to a PNG image file
// using a headless Chrome browser.
func init() {
	types.RegisterSimplify("write_image", types.SimpleAction("write_image"))
}

func HandleWriteImage(workDir string, noChrome bool, args map[string]interface{}) *types.ToolResult {
	filename, _ := args["filename"].(string)
	imgType, _ := args["type"].(string)
	content, _ := args["content"].(string)

	if filename == "" {
		return &types.ToolResult{Success: false, Error: "filename is required", Tool: "write_image", RawResult: map[string]interface{}{"filename": filename}}
	}
	if content == "" {
		return &types.ToolResult{Success: false, Error: "content is required", Tool: "write_image", RawResult: map[string]interface{}{"filename": filename}}
	}

	// Handle Graphviz DOT: pure Go rendering, no Chrome needed
	switch strings.ToLower(imgType) {
	case "dot", "graphviz":
		return handleGraphviz(workDir, filename, content, imgType)
	}

	if noChrome {
		return &types.ToolResult{
			Success:   false,
			Error:     "Chrome/Chromium browser not found on this system. Cannot render images. Install Google Chrome or Microsoft Edge.",
			Tool:      "write_image",
			RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType},
		}
	}

	// Build HTML content
	var html string
	switch strings.ToLower(imgType) {
	case "svg":
		html = fmt.Sprintf(svgTemplate, content)
	case "html":
		html = content
	case "mermaid":
		html = mermaidTemplate(content)
	case "plantuml":
		server := "http://www.plantuml.com"
		if s, ok := args["plantuml_server"].(string); ok && s != "" {
			server = strings.TrimRight(s, "/")
		}
		encoded := encodePlantUML(content)
		svg, err := fetchPlantUMLSVG(server, encoded)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("plantuml fetch failed: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
		}
		html = fmt.Sprintf(svgTemplate, svg)
	default:
		return &types.ToolResult{
			Success:   false,
			Error:     fmt.Sprintf("unsupported type '%s': must be 'svg', 'html', 'mermaid', 'dot', 'graphviz', or 'plantuml'", imgType),
			Tool:      "write_image",
			RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType},
		}
	}

	// Write HTML to temp file
	tmpFile, err := os.CreateTemp("", "write_image_*.html")
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to create temp file: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(html); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to write temp file: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Start headless Chrome
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Convert to file URL
	fileURL := "file:///" + strings.ReplaceAll(tmpPath, "\\", "/")

	var buf []byte
	err = chromedp.Run(ctx,
		// Set a generous viewport so diagrams get adequate space
		chromedp.EmulateViewport(1920, 1080),
		chromedp.Navigate(fileURL),
		chromedp.WaitReady("body"),
		// Wait for SVG elements to appear (Mermaid renders async, HTML may not have SVGs)
		// Times out after 5s to avoid hanging on HTML content without SVGs.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, err := runtime.Evaluate(`
				new Promise(function(resolve) {
					var start = Date.now();
					function check() {
						if (document.querySelectorAll('svg').length > 0 || Date.now() - start > 5000) {
							resolve();
						} else {
							setTimeout(check, 200);
						}
					}
					setTimeout(check, 300);
				});
			`).Do(ctx)
			return err
		}),
		// Expand document to full content height for full-page capture
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, err := runtime.Evaluate(
				`document.documentElement.style.minHeight = Math.min(document.documentElement.scrollHeight, 50000) + 'px'`,
			).Do(ctx)
			return err
		}),
		// Capture full page (beyond viewport)
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(true).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("screenshot failed: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}

	if len(buf) == 0 {
		return &types.ToolResult{Success: false, Error: "screenshot returned empty result", Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}

	return saveImage(workDir, filename, imgType, buf)
}

// handleGraphviz renders DOT content to PNG using pure Go graphviz library.
func handleGraphviz(workDir, filename, content, imgType string) *types.ToolResult {
	g, err := graphviz.New(context.Background())
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("graphviz init error: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}
	defer g.Close()

	graph, err := graphviz.ParseBytes([]byte(content))
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("graphviz parse error: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}

	var buf bytes.Buffer
	if err := g.Render(context.Background(), graph, graphviz.PNG, &buf); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("graphviz render error: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}

	pngBytes := buf.Bytes()
	if len(pngBytes) == 0 {
		return &types.ToolResult{Success: false, Error: "graphviz render returned empty result", Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}

	return saveImage(workDir, filename, imgType, pngBytes)
}

// encodePlantUML encodes PlantUML content using deflate + custom base64 encoding.
func encodePlantUML(content string) string {
	var buf bytes.Buffer
	zw, _ := flate.NewWriter(&buf, flate.BestCompression)
	zw.Write([]byte(content))
	zw.Close()
	encoded := base64.NewEncoding("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_").EncodeToString(buf.Bytes())
	return encoded
}

// fetchPlantUMLSVG fetches SVG content from a PlantUML server for the given encoded diagram.
func fetchPlantUMLSVG(server, encoded string) (string, error) {
	url := fmt.Sprintf("%s/plantuml/svg/%s", server, encoded)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("plantuml server unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read plantuml response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("plantuml server returned status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// saveImage writes the PNG buffer to a file and returns a success ToolResult.
func saveImage(workDir, filename, imgType string, buf []byte) *types.ToolResult {
	outPath := filepath.Join(workDir, filename)
	// Ensure parent directory exists
	if parent := filepath.Dir(outPath); parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to create output directory: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
		}
	}
	if err := os.WriteFile(outPath, buf, 0644); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to write image: %s", err.Error()), Tool: "write_image", RawResult: map[string]interface{}{"filename": filename, "size_bytes": 0, "type": imgType}}
	}
	sizeBytes := len(buf)
	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("🖼️ 图片已保存：%s（%d）", filename, sizeBytes),
		Tool:    "write_image",
		RawResult: map[string]interface{}{
			"filename":   filename,
			"size_bytes": sizeBytes,
			"type":       imgType,
		},
	}
}
