package file

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"github.com/zakahan/docx2md"
)

// ReadFileAsMarkdown reads any supported file and returns its content as markdown text.
// Supports: txt, pdf, docx, xlsx, pptx, and common code file types.
// workDir is used for storing extracted assets (e.g. docx images).
func ReadFileAsMarkdown(path, workDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".pdf":
		return readPDFAsText(path)
	case ".docx":
		return readDOCXAsText(path, workDir)
	case ".xlsx":
		return readXLSXAsText(path)
	case ".pptx":
		return readPPTXAsText(path)
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if IsBinaryBytes(data) {
			return "", fmt.Errorf("binary file not supported: %s", path)
		}
		return string(data), nil
	}
}

// readPDFAsText extracts text from PDF and formats as markdown.
func readPDFAsText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("Source: `%s`\n\n", path))

	totalPage := r.NumPage()
	for i := 1; i <= totalPage; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		if strings.TrimSpace(text) != "" {
			buf.WriteString(fmt.Sprintf("## Page %d\n\n%s\n\n", i, text))
		}
	}
	return buf.String(), nil
}

// readDOCXAsText converts docx to markdown using the zakahan/docx2md library.
// Images are extracted to <workDir>/.ide/tmp/docimg/<uuid>/ and
// referenced in markdown as relative paths from workDir.
func readDOCXAsText(path, workDir string) (string, error) {
	// Prepare image output directory under workDir with a UUID-based folder
	uid := uuid.NewString()
	imgDir := filepath.Join(workDir, ".ide", "tmp", "docimg", uid)
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create image dir: %w", err)
	}

	mdPath, mdContent, err := docx2md.DocxConvert(path, imgDir)
	if err != nil {
		os.RemoveAll(imgDir)
		return "", fmt.Errorf("failed to convert docx: %w", err)
	}
	_ = mdPath // not needed — we return content directly

	// Rewrite relative image paths to workdir-relative paths.
	// zakahan places images in <imgDir>/images/ and references them as "images/name".
	// Replace standard markdown image syntax with file_read instructions so the
	// LLM knows how to retrieve the image content.
	zakahanImgDir := filepath.Join(imgDir, "images")
	if info, err := os.Stat(zakahanImgDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(zakahanImgDir)
		if err == nil {
			relBase := filepath.Join(".ide", "tmp", "docimg", uid, "images")
			for _, entry := range entries {
				if !entry.IsDir() {
					relPath := filepath.ToSlash(filepath.Join(relBase, entry.Name()))
					instruction := fmt.Sprintf(
						"用 `file_read` 工具 读取 `%s` 获取图片内容",
						relPath,
					)
					// Replace the entire markdown image syntax.
					// zakahan uses filepath.Join("images", name) which on Windows
					// produces `images\name`, so match both separators.
					for _, sep := range []string{"/", "\\"} {
						old := "![" + entry.Name() + "](images" + sep + entry.Name() + ")"
						replacement := old + " (" + instruction + ")"
						mdContent = strings.ReplaceAll(mdContent, old, replacement)
						old2 := "![" + entry.Name() + "](./images" + sep + entry.Name() + ")"
						replacement2 := old2 + " (" + instruction + ")"
						mdContent = strings.ReplaceAll(mdContent, old2, replacement2)
					}
				}
			}
		}
	}

	return fmt.Sprintf("Source: `%s`\n\n%s", path, mdContent), nil
}

// readXLSXAsText extracts text from xlsx files.
func readXLSXAsText(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("failed to open xlsx: %w", err)
	}
	defer r.Close()

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("Source: `%s`\n\n", path))

	sharedStrings := readXLSXSharedStrings(r)

	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()

			buf.WriteString(fmt.Sprintf("## %s\n\n", f.Name))
			sheetText := extractXLSXSheetText(string(data), sharedStrings)
			buf.WriteString(sheetText)
			buf.WriteString("\n\n")
		}
	}

	return buf.String(), nil
}

func readXLSXSharedStrings(r *zip.ReadCloser) []string {
	for _, f := range r.File {
		if f.Name == "xl/sharedStrings.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil
			}
			defer rc.Close()
			data, _ := io.ReadAll(rc)
			return extractXLSXStrings(string(data))
		}
	}
	return nil
}

func extractXLSXStrings(xmlData string) []string {
	var strs struct {
		XMLName xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main sst"`
		Items   []struct {
			Text string `xml:"t"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal([]byte(xmlData), &strs); err != nil {
		return nil
	}
	result := make([]string, len(strs.Items))
	for i, item := range strs.Items {
		result[i] = item.Text
	}
	return result
}

func extractXLSXSheetText(xmlData string, sharedStrings []string) string {
	var sheet struct {
		XMLName   xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main worksheet"`
		SheetData struct {
			Rows []struct {
				Cells []struct {
					Type  string `xml:"t,attr"`
					Value string `xml:"v"`
				} `xml:"c"`
			} `xml:"row"`
		} `xml:"sheetData"`
	}
	if err := xml.Unmarshal([]byte(xmlData), &sheet); err != nil {
		return extractXMLTextSimple(xmlData, "v")
	}

	var lines []string
	for _, row := range sheet.SheetData.Rows {
		var cells []string
		for _, c := range row.Cells {
			val := c.Value
			if c.Type == "s" && sharedStrings != nil {
				idx := 0
				fmt.Sscanf(val, "%d", &idx)
				if idx >= 0 && idx < len(sharedStrings) {
					val = sharedStrings[idx]
				}
			}
			cells = append(cells, val)
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	return strings.Join(lines, "\n")
}

// readPPTXAsText extracts text from pptx files.
func readPPTXAsText(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("failed to open pptx: %w", err)
	}
	defer r.Close()

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("Source: `%s`\n\n", path))

	slideNum := 0
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideNum++
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()

			text := extractPPTXSlideText(string(data))
			if strings.TrimSpace(text) != "" {
				buf.WriteString(fmt.Sprintf("## Slide %d\n\n%s\n\n", slideNum, text))
			}
		}
	}

	if slideNum == 0 {
		return "", fmt.Errorf("no slides found in pptx")
	}
	return buf.String(), nil
}

func extractPPTXSlideText(xmlData string) string {
	var texts []string
	remaining := xmlData
	for {
		start := strings.Index(remaining, "<a:t>")
		if start == -1 {
			break
		}
		start += 5
		end := strings.Index(remaining[start:], "</a:t>")
		if end == -1 {
			break
		}
		text := strings.TrimSpace(remaining[start : start+end])
		if text != "" {
			texts = append(texts, text)
		}
		remaining = remaining[start+end+6:]
	}
	return strings.Join(texts, "\n")
}

// extractXMLTextSimple extracts text content between XML tags using substring search.
func extractXMLTextSimple(xmlData, tagName string) string {
	var texts []string
	openTag := "<" + tagName + ">"
	openTagNS := "<" + tagName + " "
	closeTag := "</" + tagName + ">"

	remaining := xmlData
	for {
		start := strings.Index(remaining, openTag)
		if start == -1 {
			start = strings.Index(remaining, openTagNS)
			if start == -1 {
				break
			}
		}
		closeBracket := strings.Index(remaining[start:], ">")
		if closeBracket == -1 {
			break
		}
		start = start + closeBracket + 1

		end := strings.Index(remaining[start:], closeTag)
		if end == -1 {
			break
		}
		text := strings.TrimSpace(remaining[start : start+end])
		if text != "" {
			texts = append(texts, text)
		}
		remaining = remaining[start+end+len(closeTag):]
	}
	return strings.Join(texts, "\n")
}

// IsBinaryBytes checks if data appears binary by searching for null bytes.
func IsBinaryBytes(data []byte) bool {
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for _, b := range data[:checkLen] {
		if b == 0 {
			return true
		}
	}
	return false
}
