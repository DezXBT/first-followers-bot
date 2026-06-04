package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TxGen is the global transaction ID generator, initialized once at startup.
var TxGen *TransactionGenerator

// TransactionGenerator produces X-Client-Transaction-Id header values
// by reverse-engineering the animation-based key derivation used by X/Twitter.
type TransactionGenerator struct {
	keyBytes        []int  // base64-decoded bytes from twitter-site-verification meta tag
	rowIndex        int    // first index extracted from ondemand JS
	keyBytesIndices []int  // remaining indices from ondemand JS
	animationKey    string // computed from SVG animation frames
	keyword         string // static keyword used in hash
	mu              sync.Mutex
	initialized     bool
}

// Init fetches x.com, parses the site verification key and animation frames,
// fetches the ondemand JS to extract byte indices, and computes the animation key.
// Thread-safe; subsequent calls are no-ops.
func Init() error {
	if TxGen != nil && TxGen.initialized {
		return nil
	}

	tg := &TransactionGenerator{keyword: "obfiowerehiring"}
	if err := tg.init(); err != nil {
		return err
	}

	TxGen = tg
	return nil
}

// Generate returns an X-Client-Transaction-Id value for the given HTTP method and path.
// Returns "" if TxGen is not initialized.
func Generate(method, path string) string {
	if TxGen == nil || !TxGen.initialized {
		return ""
	}
	return TxGen.generate(method, path)
}

// ──────────────────────────────────────────────────────────────────────────────
// Initialization
// ──────────────────────────────────────────────────────────────────────────────

func (tg *TransactionGenerator) init() error {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	if tg.initialized {
		return nil
	}

	// Step 1: Fetch x.com HTML
	html, err := fetchURL("https://x.com", map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		"Accept-Language": "en-US,en;q=0.9",
		"Referer":         "https://x.com",
	})
	if err != nil {
		return fmt.Errorf("fetch x.com: %w", err)
	}

	// Step 2: Extract twitter-site-verification meta content (base64 key)
	key, err := extractKey(html)
	if err != nil {
		return fmt.Errorf("extract key: %w", err)
	}

	// Step 3: Extract ondemand.s JS file URL
	jsURL, err := extractOndemandURL(html)
	if err != nil {
		return fmt.Errorf("extract ondemand URL: %w", err)
	}

	// Step 4: Fetch the ondemand JS
	jsContent, err := fetchURL(jsURL, nil)
	if err != nil {
		return fmt.Errorf("fetch ondemand JS: %w", err)
	}

	// Step 5: Extract key_byte_indices from JS
	rowIdx, indices, err := extractKeyByteIndices(jsContent)
	if err != nil {
		return fmt.Errorf("extract key_byte_indices: %w", err)
	}
	tg.rowIndex = rowIdx
	tg.keyBytesIndices = indices

	// Step 6: Decode key to bytes
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return fmt.Errorf("decode key: %w", err)
	}
	tg.keyBytes = make([]int, len(decoded))
	for i, b := range decoded {
		tg.keyBytes[i] = int(b)
	}

	// Step 7: Compute animation key
	animKey, err := tg.computeAnimationKey(html)
	if err != nil {
		return fmt.Errorf("compute animation key: %w", err)
	}
	tg.animationKey = animKey

	tg.initialized = true
	fmt.Printf("[transaction] X-Client-Transaction-Id generator initialized (animationKey=%s)\n", animKey)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Extraction helpers
// ──────────────────────────────────────────────────────────────────────────────

func extractKey(html string) (string, error) {
	re := regexp.MustCompile(`<meta\s+name="twitter-site-verification"\s+content="([^"]+)"`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return "", fmt.Errorf("twitter-site-verification meta tag not found")
	}
	return m[1], nil
}

func extractOndemandURL(html string) (string, error) {
	re1 := regexp.MustCompile(`,(\d+):["']ondemand\.s["']`)
	m1 := re1.FindStringSubmatch(html)
	if len(m1) < 2 {
		return "", fmt.Errorf("ondemand.s index not found in HTML")
	}
	index := m1[1]

	re2 := regexp.MustCompile(`,` + regexp.QuoteMeta(index) + `:"([0-9a-f]+)"`)
	m2 := re2.FindStringSubmatch(html)
	if len(m2) < 2 {
		return "", fmt.Errorf("ondemand.s hash not found for index %s", index)
	}
	hash := m2[1]

	return fmt.Sprintf("https://abs.twimg.com/responsive-web/client-web/ondemand.s.%sa.js", hash), nil
}

func extractKeyByteIndices(js string) (int, []int, error) {
	re := regexp.MustCompile(`\(\w{1}\[(\d{1,2})\],\s*16\)`)
	matches := re.FindAllStringSubmatch(js, -1)
	if len(matches) < 2 {
		return 0, nil, fmt.Errorf("expected at least 2 key_byte_indices matches, got %d", len(matches))
	}

	rowIndex, err := strconv.Atoi(matches[0][1])
	if err != nil {
		return 0, nil, fmt.Errorf("parse rowIndex: %w", err)
	}

	var indices []int
	for _, m := range matches[1:] {
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, nil, fmt.Errorf("parse index: %w", err)
		}
		indices = append(indices, idx)
	}

	return rowIndex, indices, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// SVG animation key computation
// ──────────────────────────────────────────────────────────────────────────────

func (tg *TransactionGenerator) computeAnimationKey(html string) (string, error) {
	svgFrames, err := parseSVGFrames(html)
	if err != nil {
		return "", fmt.Errorf("parse SVG frames: %w", err)
	}

	frameIndex := tg.keyBytes[5] % 4
	selectedFrame, ok := svgFrames[frameIndex]
	if !ok {
		return "", fmt.Errorf("SVG frame %d not found", frameIndex)
	}

	rowIdx := tg.keyBytes[tg.rowIndex] % 16
	if rowIdx >= len(selectedFrame) {
		return "", fmt.Errorf("row index %d out of range (frame %d has %d rows)", rowIdx, frameIndex, len(selectedFrame))
	}

	frameTime := 1
	for _, idx := range tg.keyBytesIndices {
		frameTime *= tg.keyBytes[idx] % 16
	}

	frameTimeF := jsRound(float64(frameTime)/10.0) * 10
	targetTime := float64(frameTimeF) / 4096.0

	frameRow := selectedFrame[rowIdx]

	return animate(frameRow, targetTime), nil
}

func parseSVGFrames(html string) (map[int][][]int, error) {
	allFrames := make(map[int][][]int)

	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("loading-x-anim-%d", i)
		svgRe := regexp.MustCompile(`id="` + id + `"[^>]*>(.*?)</svg>`)
		svgMatch := svgRe.FindStringSubmatch(html)
		if len(svgMatch) < 2 {
			continue
		}
		svgContent := svgMatch[1]

		pathRe := regexp.MustCompile(`d="([^"]+)"`)
		pathMatches := pathRe.FindAllStringSubmatch(svgContent, -1)
		if len(pathMatches) < 2 {
			continue
		}
		d := pathMatches[1][1]

		if len(d) < 10 {
			continue
		}
		d = d[9:]
		segments := strings.Split(d, "C")

		var rows [][]int
		for _, seg := range segments {
			nums := extractIntegers(seg)
			rows = append(rows, nums)
		}
		allFrames[i] = rows
	}

	if len(allFrames) == 0 {
		return nil, fmt.Errorf("no SVG animation frames found")
	}

	return allFrames, nil
}

func extractIntegers(s string) []int {
	re := regexp.MustCompile(`[^0-9\-]+`)
	cleaned := re.ReplaceAllString(s, " ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil
	}

	parts := strings.Fields(cleaned)
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

// ──────────────────────────────────────────────────────────────────────────────
// Animation math
// ──────────────────────────────────────────────────────────────────────────────

func animate(frames []int, targetTime float64) string {
	fromColor := []float64{float64(frames[0]), float64(frames[1]), float64(frames[2]), 1.0}
	toColor := []float64{float64(frames[3]), float64(frames[4]), float64(frames[5]), 1.0}
	fromRotation := []float64{0.0}
	toRotation := []float64{solve(float64(frames[6]), 60.0, 360.0, true)}

	remainingFrames := frames[7:]
	curves := make([]float64, len(remainingFrames))
	isOddVals := []float64{0.0, -1.0, 0.0, -1.0}

	for i, v := range remainingFrames {
		oddVal := isOddVals[i%len(isOddVals)]
		curves[i] = solve(float64(v), oddVal, 1.0, false)
	}

	for len(curves) < 4 {
		curves = append(curves, 0.0)
	}

	cubic := NewCubic([4]float64{curves[0], curves[1], curves[2], curves[3]})
	val := cubic.GetValue(targetTime)

	color := interpolate(fromColor, toColor, val)
	rotation := interpolate(fromRotation, toRotation, val)
	matrix := convertRotationToMatrix(rotation[0])

	var parts []string

	for i := 0; i < 3; i++ {
		c := color[i]
		if c < 0 {
			c = 0
		}
		if c > 255 {
			c = 255
		}
		parts = append(parts, fmt.Sprintf("%x", int(c)))
	}

	for _, m := range matrix {
		rounded := math.Round(m*100) / 100
		if rounded < 0 {
			rounded = -rounded
		}
		hexStr := floatToHex(rounded)
		if hexStr == "" {
			hexStr = "0"
		} else if hexStr[0] == '.' {
			hexStr = "0" + hexStr
		} else {
			hexStr = strings.ToLower(hexStr)
		}
		parts = append(parts, hexStr)
	}

	parts = append(parts, "0", "0")

	result := strings.Join(parts, "")
	result = strings.ReplaceAll(result, ".", "")
	result = strings.ReplaceAll(result, "-", "")

	return result
}

func solve(value, minVal, maxVal float64, rounding bool) float64 {
	result := value*(maxVal-minVal)/255.0 + minVal
	if rounding {
		return math.Floor(result)
	}
	return math.Round(result*100) / 100
}

func floatToHex(x float64) string {
	intPart := int64(x)
	fracPart := x - float64(intPart)

	var intStr string
	if intPart == 0 {
		intStr = "0"
	} else {
		for intPart > 0 {
			rem := intPart % 16
			if rem < 10 {
				intStr = string(rune('0'+rem)) + intStr
			} else {
				intStr = string(rune('A'+rem-10)) + intStr
			}
			intPart /= 16
		}
	}

	if fracPart < 1e-10 {
		return intStr
	}

	var fracStr string
	for i := 0; i < 16 && fracPart > 1e-10; i++ {
		fracPart *= 16
		digit := int64(fracPart)
		fracPart -= float64(digit)
		if digit < 10 {
			fracStr += string(rune('0' + digit))
		} else {
			fracStr += string(rune('A' + digit - 10))
		}
	}

	return intStr + "." + fracStr
}

func interpolate(from, to []float64, f float64) []float64 {
	result := make([]float64, len(from))
	for i := range from {
		result[i] = from[i]*(1-f) + to[i]*f
	}
	return result
}

func convertRotationToMatrix(rotation float64) [4]float64 {
	rad := rotation * math.Pi / 180.0
	return [4]float64{math.Cos(rad), -math.Sin(rad), math.Sin(rad), math.Cos(rad)}
}

// ──────────────────────────────────────────────────────────────────────────────
// Cubic Bézier
// ──────────────────────────────────────────────────────────────────────────────

type Cubic struct {
	curves [4]float64
}

func NewCubic(curves [4]float64) *Cubic {
	return &Cubic{curves: curves}
}

func (c *Cubic) GetValue(time float64) float64 {
	if time <= 0 {
		return 0
	}
	if time >= 1 {
		return 1
	}

	low := 0.0
	high := 1.0
	var mid float64

	for i := 0; i < 32; i++ {
		mid = (low + high) / 2.0
		x := calcBezier(c.curves[0], c.curves[2], mid)
		if x < time {
			low = mid
		} else {
			high = mid
		}
	}

	return calcBezier(c.curves[1], c.curves[3], mid)
}

func calcBezier(a, b, m float64) float64 {
	return 3.0*a*(1-m)*(1-m)*m + 3.0*b*(1-m)*m*m + m*m*m
}

// ──────────────────────────────────────────────────────────────────────────────
// Transaction ID generation
// ──────────────────────────────────────────────────────────────────────────────

func (tg *TransactionGenerator) generate(method, path string) string {
	timeNow := time.Now().Unix() - 1682924400

	timeNowBytes := make([]byte, 4)
	for i := 0; i < 4; i++ {
		timeNowBytes[i] = byte((timeNow >> (i * 8)) & 0xFF)
	}

	shaInput := fmt.Sprintf("%s!%s!%d%s%s", method, path, timeNow, tg.keyword, tg.animationKey)
	shaHash := sha256.Sum256([]byte(shaInput))

	payload := make([]int, 0, len(tg.keyBytes)+4+16+1)
	for _, b := range tg.keyBytes {
		payload = append(payload, b)
	}
	for _, b := range timeNowBytes {
		payload = append(payload, int(b))
	}
	for i := 0; i < 16; i++ {
		payload = append(payload, int(shaHash[i]))
	}
	payload = append(payload, 3)

	randomByte := make([]byte, 1)
	rand.Read(randomByte)
	rb := int(randomByte[0])

	encoded := make([]byte, len(payload)+1)
	encoded[0] = byte(rb)
	for i, p := range payload {
		encoded[i+1] = byte(p ^ rb)
	}

	result := base64.StdEncoding.EncodeToString(encoded)
	result = strings.TrimRight(result, "=")

	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Utility functions
// ──────────────────────────────────────────────────────────────────────────────

func jsRound(num float64) int {
	x := math.Floor(num)
	if num-x >= 0.5 {
		x = math.Ceil(num)
	}
	return int(math.Copysign(x, num))
}

func fetchURL(url string, headers map[string]string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if headers != nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	return string(body), nil
}
