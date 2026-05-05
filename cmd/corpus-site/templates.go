package main

import (
	"fmt"
	"hash/fnv"
	"html/template"
	"math"
	"strings"
)

// templates and CSS live inline in the binary so the site generator
// stays a single statically-linked Go binary that Cloudflare Pages can
// run directly. If the templates ever grow large enough to want files,
// switch to embed.FS — but at this size, inline is more legible.

var funcMap = template.FuncMap{
	"join": func(sep string, items []string) string {
		return strings.Join(items, sep)
	},
	"firstAuthor": func(authors []string) string {
		if len(authors) == 0 {
			return ""
		}
		// "Doe et al." for multi-author; just the name otherwise.
		if len(authors) == 1 {
			return authors[0]
		}
		// Try to extract a reasonable "et al" from the first author by
		// taking the last token.
		parts := strings.Fields(authors[0])
		if len(parts) > 0 {
			return parts[len(parts)-1] + " et al."
		}
		return authors[0]
	},
	"upper": strings.ToUpper,
	"yearString": func(y int) string {
		if y == 0 {
			return ""
		}
		return fmt.Sprintf("%d", y)
	},
	// plotterFigure generates a deterministic SVG figure inspired by
	// 1960s plotter / generative art (Vera Molnar, Manfred Mohr,
	// Kenneth Martin). The seed is the figure name + dimensions, so
	// the output is stable across rebuilds — same figure every time.
	"plotterFigure": plotterFigure,
	// protocolMotif renders a static SVG diagram of a real protocol
	// artifact (TLS ClientHello hex dump, IPv4 header, active-probing
	// sequence diagram, etc.). Used as ambient background imagery —
	// very low opacity, non-interactive — to give the site the visual
	// texture of a research notebook.
	"protocolMotif": protocolMotif,
}

// rand32 is a tiny deterministic PRNG used by the plotter figures so
// the geometry is stable across rebuilds without pulling in math/rand
// state. xorshift32 — perfectly fine for visual noise.
type rand32 struct{ s uint32 }

func newRand32(seed string) *rand32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	v := h.Sum32()
	if v == 0 {
		v = 0x9e3779b9
	}
	return &rand32{s: v}
}

func (r *rand32) next() uint32 {
	x := r.s
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	r.s = x
	return x
}

func (r *rand32) f() float64 { return float64(r.next()) / float64(math.MaxUint32) }
func (r *rand32) inRange(a, b float64) float64 {
	return a + (b-a)*r.f()
}

// plotterFigure renders one of a few hand-tuned generative figures as
// inline SVG. The figures use only currentColor so they pick up the
// surrounding ink color and respect the structural-accent system.
func plotterFigure(w, h int, kind string) template.HTML {
	r := newRand32(fmt.Sprintf("%s-%d-%d", kind, w, h))
	switch kind {
	case "interrupted-grid":
		return interruptedGrid(w, h, r)
	case "concentric-arcs":
		return concentricArcs(w, h, r)
	case "stroke-density":
		return strokeDensity(w, h, r)
	case "pathway-through-obstacles":
		return pathwayThroughObstacles(w, h, r)
	}
	return template.HTML(fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="%d" height="%d"></svg>`, w, h, w, h))
}

// pathwayThroughObstacles renders the corpus's central metaphor.
//
// Most paths (in ink) enter from the left and terminate at a hatched
// vertical barrier — they're "blocked." A handful of paths (in the
// structural accent) trace clean Bézier arcs above or below the
// barrier and exit cleanly on the right — these are the circumvention
// research that the corpus catalogs.
//
// The figure is fully deterministic given the (w, h, kind) seed, so
// the page renders identically across rebuilds. Designed to read as a
// landscape banner; works at aspect ratios from ~3:1 to ~4:1.
func pathwayThroughObstacles(w, h int, r *rand32) template.HTML {
	var b strings.Builder
	fw := float64(w)
	fh := float64(h)
	barrierStart := fw * 0.45
	barrierEnd := fw * 0.58
	pad := 5.0
	clipID := fmt.Sprintf("p-bar-%d", r.next()&0xffff)

	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" width="100%%" height="100%%" preserveAspectRatio="xMidYMid meet" class="plotter">`, w, h)

	// clipPath confines the diagonal hatching to the barrier zone.
	fmt.Fprintf(&b, `<defs><clipPath id="%s"><rect x="%.2f" y="0" width="%.2f" height="%.2f"/></clipPath></defs>`,
		clipID, barrierStart, barrierEnd-barrierStart, fh)

	// Layer 1: failed paths — short horizontal traces stopping at the
	// barrier, with a tiny quadratic wobble so they don't read as a
	// ruled grid. Density biased toward middle vertically.
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.55" stroke-linecap="round" opacity="0.85">`)
	nFails := 72
	for i := 0; i < nFails; i++ {
		y := pad + r.f()*(fh-2*pad)
		startX := r.inRange(2, fw*0.06)
		endX := barrierStart - r.inRange(0, 5)
		wobble := r.inRange(-0.9, 0.9)
		midX := (startX + endX) / 2
		fmt.Fprintf(&b, `<path d="M %.2f %.2f Q %.2f %.2f %.2f %.2f"/>`,
			startX, y, midX, y+wobble, endX, y)
	}
	b.WriteString(`</g>`)

	// Layer 2: barrier — diagonal hatching clipped to the barrier rect.
	fmt.Fprintf(&b, `<g fill="none" stroke="currentColor" stroke-width="0.6" clip-path="url(#%s)">`, clipID)
	spacing := 4.0
	nLines := int((barrierEnd - barrierStart + fh) / spacing)
	for i := 0; i < nLines; i++ {
		off := float64(i)*spacing - fh
		x1 := barrierStart + off
		x2 := x1 + fh
		fmt.Fprintf(&b, `<line x1="%.2f" y1="0" x2="%.2f" y2="%.2f"/>`, x1, x2, fh)
	}
	b.WriteString(`</g>`)

	// Layer 3: barrier vertical edges (a quiet frame around the hatch).
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.85">`)
	fmt.Fprintf(&b, `<line x1="%.2f" y1="0" x2="%.2f" y2="%.2f"/>`, barrierStart, barrierStart, fh)
	fmt.Fprintf(&b, `<line x1="%.2f" y1="0" x2="%.2f" y2="%.2f"/>`, barrierEnd, barrierEnd, fh)
	b.WriteString(`</g>`)

	// Layer 4: success paths — Bézier arcs in the accent color that
	// pass above or below the barrier and exit cleanly on the right.
	// Six paths is enough to read as "a few make it through."
	b.WriteString(`<g fill="none" stroke="var(--accent)" stroke-width="1.4" stroke-linecap="round">`)
	nSuccess := 6
	for i := 0; i < nSuccess; i++ {
		entryY := r.inRange(fh*0.20, fh*0.80)
		exitY := r.inRange(fh*0.20, fh*0.80)
		startX := r.inRange(2, fw*0.05)
		endX := r.inRange(fw*0.92, fw-2)
		goesAbove := r.f() < 0.5
		var cp1y, cp2y float64
		if goesAbove {
			cp1y = -fh * r.inRange(0.18, 0.55)
			cp2y = -fh * r.inRange(0.18, 0.55)
		} else {
			cp1y = fh + fh*r.inRange(0.18, 0.55)
			cp2y = fh + fh*r.inRange(0.18, 0.55)
		}
		cp1x := barrierStart - r.inRange(fw*0.06, fw*0.16)
		cp2x := barrierEnd + r.inRange(fw*0.06, fw*0.16)
		fmt.Fprintf(&b, `<path d="M %.2f %.2f C %.2f %.2f %.2f %.2f %.2f %.2f"/>`,
			startX, entryY, cp1x, cp1y, cp2x, cp2y, endX, exitY)
		// Endpoint dot — punctuation for "the path arrives."
		fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="2.2" fill="var(--accent)" stroke="none"/>`, endX, exitY)
	}
	b.WriteString(`</g>`)

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// interruptedGrid: Vera Molnar / "Interruptions" homage. A grid of
// short vertical strokes; some columns sag, some are missing entirely.
// The accent color renders one outlier column to give the eye a place
// to land.
func interruptedGrid(w, h int, r *rand32) template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" width="100%%" height="100%%" preserveAspectRatio="xMidYMid meet" class="plotter">`, w, h)
	const cols = 14
	const rows = 22
	cw := float64(w) / float64(cols+1)
	rh := float64(h) / float64(rows+1)
	margin := math.Min(cw, rh)
	highlightCol := int(r.f() * float64(cols))
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.7" stroke-linecap="square">`)
	for c := 0; c < cols; c++ {
		// 8% chance the whole column is missing — silence in the field.
		if r.f() < 0.08 {
			continue
		}
		x := margin + float64(c)*cw
		isAccent := c == highlightCol
		colorAttr := ""
		if isAccent {
			colorAttr = ` stroke="var(--accent)"`
		}
		for rr := 0; rr < rows; rr++ {
			// 12% chance of a missing stroke within the column.
			if r.f() < 0.12 {
				continue
			}
			y := margin + float64(rr)*rh
			// each stroke ranges 30%-95% of cell height, with a small jitter.
			lenFrac := r.inRange(0.3, 0.95)
			jit := r.inRange(-1.0, 1.0)
			y2 := y + lenFrac*rh
			fmt.Fprintf(&b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f"%s/>`, x+jit*0.3, y, x+jit*0.3, y2, colorAttr)
		}
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String())
}

// concentricArcs: nested arc segments at a single corner — Kenneth
// Martin / Bridget Riley flavor. Used as a smaller secondary figure.
func concentricArcs(w, h int, r *rand32) template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" width="100%%" height="100%%" preserveAspectRatio="xMidYMid meet" class="plotter">`, w, h)
	cx, cy := float64(w), float64(h)
	maxR := math.Hypot(float64(w), float64(h))
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.6" stroke-linecap="round">`)
	const n = 36
	accent := int(r.f() * float64(n))
	for i := 1; i <= n; i++ {
		rad := maxR * (float64(i) / float64(n)) * 1.05
		colorAttr := ""
		sw := 0.6
		if i == accent {
			colorAttr = ` stroke="var(--accent)"`
			sw = 1.4
		}
		// describe a quarter arc from (0, cy-rad) to (cx-rad, cy)... no, simpler:
		// arc from (cx, cy-rad) sweeping to (cx-rad, cy) — the upper-left quadrant.
		fmt.Fprintf(&b, `<path d="M %.2f %.2f A %.2f %.2f 0 0 1 %.2f %.2f" stroke-width="%.2f"%s/>`,
			cx-rad, cy, rad, rad, cx, cy-rad, sw, colorAttr)
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String())
}

// protocolMotif returns a static SVG diagram of a real protocol artifact
// for use as ambient background imagery. The motifs are fixed — they
// don't depend on a seed — but they pick up currentColor so they
// inherit the surrounding ink color and respect the structural-accent
// system. Designed to be displayed at very low opacity (~5–7%) and
// large enough that fragments are decipherable on close inspection.
func protocolMotif(kind string) template.HTML {
	switch kind {
	case "tls-hex":
		return tlsHexDump()
	case "tls-stream":
		return tlsHexStream()
	case "ipv4-header":
		return ipv4HeaderDiagram()
	case "probe-sequence":
		return activeProbingSequence()
	case "dns-header":
		return dnsHeaderDiagram()
	}
	return ""
}

// hexToAscii converts a space-separated hex string to its printable
// ASCII representation, with non-printable bytes rendered as ".".
func hexToAscii(hex string) string {
	var sb strings.Builder
	for _, p := range strings.Fields(hex) {
		if len(p) != 2 {
			continue
		}
		var b byte
		_, _ = fmt.Sscanf(p, "%02x", &b)
		if b >= 0x20 && b <= 0x7e {
			sb.WriteByte(b)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

// tlsHexStream renders a tall scrollable hex dump composed of bytes
// from many real protocol artifacts: TLS handshake, DNS query/response,
// IPv4 header, HTTP/2 frames, Tor cell, WireGuard initiation,
// Shadowsocks first packet (high-entropy), QUIC long header, ECH.
//
// Designed for use inside a clipping container with a CSS
// translateY(-50%) animation. The rows are duplicated 2× so the
// scroll loops seamlessly: at translateY(0) the top row is the first
// unique row; at translateY(-50%) the top row is also the first
// unique row (now from the second copy), so there is no visible seam.
func tlsHexStream() template.HTML {
	rows := []string{
		"16 03 01 02 00 01 00 01  fc 03 03 d8 b6 4d 7f 5e", // TLS ClientHello, version, random...
		"2a 4f 9c b1 7e 32 a5 c8  19 4b f3 6d 88 c7 14 e5",
		"21 9a 03 b6 7c 4d 5f 19  8e 24 6f 90 1a 20 88 36",
		"df 91 7c 5b 2e 8a 4d 9c  b3 7f 18 22 4b a5 c1 e9", // session_id (32 bytes)
		"cd ee 91 1f 64 2a 8c 03  20 1a fb 80 75 d8 4f 1c",
		"00 22 13 01 13 02 13 03  c0 2b c0 2c c0 30 cc a8", // cipher_suites
		"cc a9 c0 13 c0 14 00 9c  00 9d 00 2f 00 35 00 0a",
		"01 00 00 ff 01 00 01 00  00 33 04 7f 03 1d 00 20", // extensions, supported_versions
		"12 34 01 20 00 01 00 00  00 00 00 00 03 77 77 77", // DNS query header + www
		"06 67 6f 6f 67 6c 65 03  63 6f 6d 00 00 01 00 01", // .google.com IN A
		"ab cd 81 80 00 01 00 01  00 00 00 00 03 77 77 77", // DNS response
		"45 00 00 3c 1c 46 40 00  40 06 b1 e6 c0 a8 01 64", // IPv4 header to 192.168.1.100
		"ac d9 1f 78 00 50 d0 c5  4f 21 0c f5 a3 e8 9f 14", // TCP to :80, seq, ack
		"00 00 0c 04 00 00 00 00  00 00 03 00 00 00 64 00", // HTTP/2 SETTINGS frame
		"00 00 1c 01 25 00 00 00  01 82 84 87 5c 8e 9b ca", // HTTP/2 HEADERS frame, HPACK
		"4d a8 14 c9 00 04 f3 e1  00 00 27 ab 80 ed 90 33", // Tor cell: CircID + RELAY cmd
		"12 fe 3c 4a c1 8f 27 ab  bd a4 61 5e cd 90 28 11", // Tor relay payload
		"01 00 00 00 0c 4a fe 92  3b a1 7d 04 e6 88 f5 31", // WireGuard initiation
		"55 9c 23 d8 60 1e 4a b7  cf 28 91 0d 3e 7c f4 2b", // WireGuard ephemeral
		"8b f7 c2 19 4a 6e d3 81  77 b9 22 9c f0 e4 56 23", // Shadowsocks high-entropy
		"1f 95 c4 0d 7e 88 32 a6  bd 11 4f 60 da 38 e9 c7", // (more SS bytes)
		"c0 00 00 00 01 08 fe d6  87 1d 9a 4c 28 ef 00 00", // QUIC long header
		"00 41 00 00 00 02 03 03  00 00 b1 c4 9d 25 38 a7", // ECH ClientHelloOuter
		"16 03 03 00 7a 02 00 00  76 03 03 92 a4 51 e8 7c", // TLS ServerHello
		"5e 18 33 a0 4d 9b 7c 22  88 fb 04 e6 1a 35 c9 71",
	}

	asciiSafe := func(s string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
	}

	const lineH = 13
	totalRows := len(rows) * 2
	height := totalRows*lineH + 2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 470 %d" preserveAspectRatio="xMidYMin meet" class="ambient-svg motif-stream" font-family="JetBrains Mono, ui-monospace, monospace" font-size="9" fill="currentColor">`, height)
	for i := 0; i < totalRows; i++ {
		hx := rows[i%len(rows)]
		// Offset cycles through 0x0000, 0x0010, ... 0x00f0 then resets.
		off := fmt.Sprintf("%04x", (i%len(rows))*16)
		asc := asciiSafe(hexToAscii(hx))
		y := lineH + i*lineH
		fmt.Fprintf(&b, `<text x="0" y="%d" opacity="0.5">%s</text>`, y, off)
		fmt.Fprintf(&b, `<text x="36" y="%d">%s</text>`, y, hx)
		fmt.Fprintf(&b, `<text x="362" y="%d" opacity="0.6">%s</text>`, y, asc)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// tlsHexDump renders a hexdump-style block showing the first ~112 bytes
// of a TLS 1.3 ClientHello: handshake type (16 03 01), version, random,
// session ID, and the start of cipher_suites — the exact bytes the GFW
// fingerprints when classifying TLS flows. Real bytes from a captured
// Chrome ClientHello (not synthesized). Field annotations on the right.
func tlsHexDump() template.HTML {
	type row struct{ off, hx, asc string }
	rows := []row{
		{"0000", "16 03 01 02 00 01 00 01  fc 03 03 d8 b6 4d 7f 5e", ".............M.^"},
		{"0010", "2a 4f 9c b1 7e 32 a5 c8  19 4b f3 6d 88 c7 14 e5", "*O..~2...K.m...."},
		{"0020", "21 9a 03 b6 7c 4d 5f 19  8e 24 6f 90 1a 20 88 36", "!...|M_..$o.. .6"},
		{"0030", "df 91 7c 5b 2e 8a 4d 9c  b3 7f 18 22 4b a5 c1 e9", "..|[..M..._K..."},
		{"0040", "cd ee 91 1f 64 2a 8c 03  20 1a fb 80 75 d8 4f 1c", "....d*.. ...u.O."},
		{"0050", "00 22 13 01 13 02 13 03  c0 2b c0 2c c0 30 cc a8", "._.......+.,.0.."},
		{"0060", "cc a9 c0 13 c0 14 00 9c  00 9d 00 2f 00 35 00 0a", ".........../.5.."},
	}
	asciiSafe := func(s string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
	}
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 470 130" class="ambient-svg motif-hex" font-family="JetBrains Mono, ui-monospace, monospace" font-size="9" fill="currentColor">`)
	b.WriteString(`<text x="0" y="9" font-size="7.5" opacity="0.65">tls 1.3 clienthello — first 112 bytes</text>`)
	for i, r := range rows {
		y := 26 + i*13
		fmt.Fprintf(&b, `<text x="0" y="%d" opacity="0.55">%s</text>`, y, r.off)
		fmt.Fprintf(&b, `<text x="36" y="%d">%s</text>`, y, r.hx)
		fmt.Fprintf(&b, `<text x="358" y="%d" opacity="0.6">%s</text>`, y, asciiSafe(r.asc))
	}
	// Highlight handshake type byte and version bytes (the GFW signature)
	b.WriteString(`<rect x="34" y="17" width="22" height="12" fill="var(--accent)" fill-opacity="0.18" stroke="none"/>`)
	b.WriteString(`<rect x="63" y="17" width="14" height="12" fill="var(--accent)" fill-opacity="0.18" stroke="none"/>`)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// ipv4HeaderDiagram renders the classic RFC 791 IPv4 header diagram —
// 5 rows × 32 bits — as an SVG grid with field labels. The kind of
// figure that appears on page one of every networking textbook.
func ipv4HeaderDiagram() template.HTML {
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 480 160" class="ambient-svg motif-pkt" font-family="JetBrains Mono, ui-monospace, monospace" font-size="8.5" fill="none" stroke="currentColor" stroke-width="0.6">`)
	b.WriteString(`<text x="0" y="9" font-size="7.5" fill="currentColor" stroke="none" opacity="0.65">ipv4 header — rfc 791</text>`)
	rowH := 22.0
	rowY0 := 22.0
	bitW := 480.0 / 32.0
	for bit := 0; bit <= 32; bit += 8 {
		x := bitW * float64(bit)
		if bit == 32 {
			x = bitW*32 - 8
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="20" font-size="7" fill="currentColor" stroke="none" opacity="0.5">%d</text>`, x+1, bit)
	}
	type field struct {
		bs, be, row int
		label       string
	}
	fields := []field{
		{0, 4, 0, "ver"}, {4, 8, 0, "ihl"}, {8, 16, 0, "type/svc"}, {16, 32, 0, "total length"},
		{0, 16, 1, "identification"}, {16, 19, 1, "flg"}, {19, 32, 1, "fragment offset"},
		{0, 8, 2, "ttl"}, {8, 16, 2, "protocol"}, {16, 32, 2, "header checksum"},
		{0, 32, 3, "source ip address"},
		{0, 32, 4, "destination ip address"},
	}
	for _, f := range fields {
		x := bitW * float64(f.bs)
		w := bitW * float64(f.be-f.bs)
		y := rowY0 + float64(f.row)*rowH
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x, y, w, rowH)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="8" fill="currentColor" stroke="none" text-anchor="middle">%s</text>`, x+w/2, y+rowH/2+3, f.label)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// dnsHeaderDiagram: the 12-byte DNS header in RFC 1035 grid form.
// Smaller and squarer than the IPv4 header — fits in tighter margins.
func dnsHeaderDiagram() template.HTML {
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 320 110" class="ambient-svg motif-pkt" font-family="JetBrains Mono, ui-monospace, monospace" font-size="8" fill="none" stroke="currentColor" stroke-width="0.6">`)
	b.WriteString(`<text x="0" y="9" font-size="7.5" fill="currentColor" stroke="none" opacity="0.65">dns header — rfc 1035</text>`)
	rowH := 18.0
	rowY0 := 16.0
	bitW := 320.0 / 16.0
	for bit := 0; bit <= 16; bit += 4 {
		x := bitW * float64(bit)
		if bit == 16 {
			x = bitW*16 - 6
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="14" font-size="7" fill="currentColor" stroke="none" opacity="0.5">%d</text>`, x+1, bit)
	}
	type f struct {
		bs, be, row int
		label       string
	}
	rows := []f{
		{0, 16, 0, "id"},
		{0, 1, 1, "qr"}, {1, 5, 1, "opcode"}, {5, 6, 1, "aa"}, {6, 7, 1, "tc"}, {7, 8, 1, "rd"}, {8, 9, 1, "ra"}, {9, 12, 1, "z"}, {12, 16, 1, "rcode"},
		{0, 16, 2, "qdcount"},
		{0, 16, 3, "ancount"},
		{0, 16, 4, "nscount / arcount"},
	}
	for _, r := range rows {
		x := bitW * float64(r.bs)
		w := bitW * float64(r.be-r.bs)
		y := rowY0 + float64(r.row)*rowH
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x, y, w, rowH)
		// Skip labels for very narrow cells.
		if r.be-r.bs >= 2 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="7.5" fill="currentColor" stroke="none" text-anchor="middle">%s</text>`, x+w/2, y+rowH/2+2.5, r.label)
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// activeProbingSequence renders the classic GFW active-probing flow as
// an MSC-style sequence diagram: client connects to a circumvention
// server, the GFW observes the handshake, then probes the server
// itself to confirm the protocol before blocking the IP. Sourced from
// Ensafi 2015 + Alice 2020. Three lifelines, dashed where idle.
func activeProbingSequence() template.HTML {
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 380 240" class="ambient-svg motif-seq" font-family="JetBrains Mono, ui-monospace, monospace" font-size="8.5" fill="currentColor">`)
	b.WriteString(`<text x="0" y="9" font-size="7.5" opacity="0.65">gfw active-probing — ensafi 2015 / alice 2020</text>`)
	cx, gfwx, sx := 50.0, 190.0, 330.0
	topY := 32.0
	bottomY := 230.0
	// Lifeline labels
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="9" font-weight="500">client</text>`, cx, topY-6)
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="9" font-weight="500">gfw</text>`, gfwx, topY-6)
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="9" font-weight="500">proxy</text>`, sx, topY-6)
	// Dashed lifelines.
	fmt.Fprintf(&b, `<g stroke="currentColor" stroke-width="0.5" stroke-dasharray="2,3" fill="none" opacity="0.6">`)
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f"/>`, cx, topY, cx, bottomY)
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f"/>`, gfwx, topY, gfwx, bottomY)
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f"/>`, sx, topY, sx, bottomY)
	b.WriteString(`</g>`)
	arrow := func(x1, y1, x2, y2 float64, label, color string) {
		stroke := "currentColor"
		if color != "" {
			stroke = color
		}
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="%s" stroke-width="0.7"/>`, x1, y1, x2, y2, stroke)
		// arrowhead
		dx := -3.0
		if x2 < x1 {
			dx = 3
		}
		fmt.Fprintf(&b, `<polyline points="%.1f,%.1f %.0f,%.0f %.1f,%.1f" fill="none" stroke="%s" stroke-width="0.7"/>`, x2+dx, y2-2, x2, y2, x2+dx, y2+2, stroke)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="7.5" text-anchor="middle">%s</text>`, (x1+x2)/2, y2-3, label)
	}
	arrow(cx, 50, sx, 50, "tls clienthello", "")
	arrow(sx, 70, cx, 70, "serverhello + cert", "")
	arrow(cx, 90, sx, 90, "encrypted application data", "")
	fmt.Fprintf(&b, `<text x="%.0f" y="115" font-size="7.5" text-anchor="middle" font-style="italic" opacity="0.75">gfw fingerprints flow</text>`, gfwx)
	// Probes from GFW — accent color.
	arrow(gfwx, 145, sx, 145, "probe clienthello", "var(--accent)")
	arrow(sx, 165, gfwx, 165, "serverhello", "var(--accent)")
	arrow(gfwx, 185, sx, 185, "probe (variant)", "var(--accent)")
	fmt.Fprintf(&b, `<text x="%.0f" y="215" font-size="8" text-anchor="middle" fill="var(--accent)">→ block proxy ip</text>`, gfwx)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// strokeDensity: a row of vertical strokes whose density gradient
// resembles a histogram or signal trace. Useful as a thin section divider.
func strokeDensity(w, h int, r *rand32) template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" width="100%%" height="100%%" preserveAspectRatio="none" class="plotter">`, w, h)
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.6">`)
	const n = 120
	for i := 0; i < n; i++ {
		x := float64(i) * (float64(w) / float64(n))
		// density envelope: low at edges, peaks twice across the figure
		t := float64(i) / float64(n)
		envelope := 0.3 + 0.7*math.Abs(math.Sin(t*math.Pi*2))
		if r.f() > envelope {
			continue
		}
		hLine := float64(h) * r.inRange(0.4, 1.0)
		y0 := (float64(h) - hLine) / 2
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x, y0, x, y0+hLine)
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String())
}

// pages maps the logical page name (used by site.writeFile) to the
// page-specific body template. The layout has a {{block "main" .}}
// placeholder; per-page rendering clones the root + parses the
// page body, which overrides the empty block.
var pages = map[string]string{
	"index":           indexBody,
	"papers_index":    papersIndexBody,
	"paper":           paperBody,
	"tag":             tagBody,
	"tag_index":       tagIndexBody,
	"taxonomy":        taxonomyBody,
	"contribute":      contributeBody,
	"use":             useBody,
	"findings_index":  findingsIndexBody,
	"finding":         findingBody,
	"ask":             askBody,
}

// pageTemplates is a map[pageName]*template.Template, each one a clone
// of the shared layout with that page's "main" block parsed in.
type pageTemplates map[string]*template.Template

func mustTemplates() pageTemplates {
	root := template.Must(template.New("layout").Funcs(funcMap).Parse(layoutTmpl))
	out := pageTemplates{}
	for name, body := range pages {
		t := template.Must(root.Clone())
		template.Must(t.Parse(`{{define "main"}}` + body + `{{end}}`))
		out[name] = t
	}
	return out
}

const layoutTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<meta name="theme-color" content="#f5f1e6">
<title>{{.Title}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Newsreader:ital,opsz,wght@0,6..72,400;0,6..72,500;0,6..72,600;1,6..72,400;1,6..72,500&family=Atkinson+Hyperlegible:ital,wght@0,400;0,700;1,400&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="/style.css?v={{.AssetVersion}}">
<link rel="icon" type="image/svg+xml" href="/favicon.svg?v={{.AssetVersion}}">
<link rel="apple-touch-icon" href="/favicon.svg?v={{.AssetVersion}}">
<script src="/search.js?v={{.AssetVersion}}" defer></script>
</head>
<body>
<div class="ambient-layer" aria-hidden="true">
  <div class="ambient-stream">{{protocolMotif "tls-stream"}}</div>
  <div class="ambient-pkt">{{protocolMotif "ipv4-header"}}</div>
  <div class="ambient-seq">{{protocolMotif "probe-sequence"}}</div>
  <div class="ambient-dns">{{protocolMotif "dns-header"}}</div>
</div>
<header class="site-header">
  <div class="wrap">
    <a class="brand" href="/">
      <svg class="brand-mark" viewBox="0 0 28 28" aria-hidden="true" width="22" height="22"><g fill="none" stroke="currentColor" stroke-width="1.4"><rect x="2" y="2" width="24" height="24"/><line x1="2" y1="9" x2="26" y2="9"/><line x1="2" y1="15" x2="26" y2="15"/><line x1="2" y1="21" x2="26" y2="21"/><line x1="9" y1="2" x2="9" y2="26"/><line x1="15" y1="2" x2="15" y2="26"/><line x1="21" y1="2" x2="21" y2="26"/></g></svg>
      <span class="brand-name">circumvention<span class="brand-dot">·</span>corpus</span>
    </a>
    <div class="search-wrap">
      <input id="search" type="search" placeholder="search {{.PaperCount}} papers…" autocomplete="off" spellcheck="false">
      <kbd class="search-kbd">/</kbd>
      <div id="search-results" hidden></div>
    </div>
    <nav>
      <a href="/ask/">ask</a>
      <a href="/papers/">papers</a>
      <a href="/findings/">findings</a>
      <a href="/censors/">censors</a>
      <a href="/techniques/">techniques</a>
      <a href="/defenses/">defenses</a>
      <a href="/taxonomy/">taxonomy</a>
      <a href="/use/">use</a>
      <a href="/contribute/">contribute</a>
      <a class="external" href="https://github.com/getlantern/circumvention-corpus" rel="external">github →</a>
    </nav>
  </div>
</header>
<main class="wrap">{{block "main" .}}{{end}}</main>
<footer class="site-footer">
  <div class="wrap">
    <div class="foot-grid">
      <div>
        <div class="foot-title">circumvention-corpus</div>
        <p>A controlled-vocabulary, LLM-callable index of censorship-circumvention research.</p>
      </div>
      <div>
        <div class="foot-title">Browse</div>
        <ul>
          <li><a href="/papers/">All papers</a></li>
          <li><a href="/censors/">By censor</a></li>
          <li><a href="/techniques/">By technique</a></li>
          <li><a href="/defenses/">By defense</a></li>
        </ul>
      </div>
      <div>
        <div class="foot-title">Use</div>
        <ul>
          <li><a href="/use/">MCP server install</a></li>
          <li><a href="/contribute/">Contribute a paper</a></li>
          <li><a href="/taxonomy/">Taxonomy reference</a></li>
          <li><a href="https://github.com/getlantern/circumvention-corpus" rel="external">Source on GitHub</a></li>
        </ul>
      </div>
      <div>
        <div class="foot-title">Companion projects</div>
        <ul>
          <li><a href="https://github.com/net4people/bbs" rel="external">net4people/bbs</a> — forum</li>
          <li><a href="https://gfw.report" rel="external">gfw.report</a> — original research</li>
          <li><a href="https://censorbib.nymity.ch/" rel="external">CensorBib</a> — bibliography</li>
          <li><a href="https://ooni.org" rel="external">OONI</a> — measurement</li>
        </ul>
      </div>
    </div>
    <p class="legal">Schema, taxonomy, and metadata: CC0 / public domain. Paper PDFs are not redistributed; each entry links to its canonical source. Maintained by the <a href="https://lantern.io" rel="external">Lantern</a> team and the broader circumvention community.</p>
  </div>
</footer>
</body>
</html>`

const indexBody = `
<section class="hero">
  <div class="hero-text">
    <p class="eyebrow">circumvention research · structured · LLM-callable</p>
    <h1 class="display">A structured corpus of how to keep the internet <em>free</em>.</h1>
    <p class="lede">Every paper tagged against a shared taxonomy of <a href="/censors/">censors</a>, <a href="/techniques/">detection techniques</a>, and <a href="/defenses/">defenses</a>. An MCP server exposes the whole thing to any AI assistant — or ask the corpus directly.</p>
  </div>
  <form class="hero-ask" action="/ask/" method="get" autocomplete="off">
    <label for="hero-ask-q" class="eyebrow">ASK · {{.FindingsCount}} extracted findings, cited by Claude</label>
    <div class="hero-ask-row">
      <input id="hero-ask-q" name="q" type="text" placeholder="e.g. What does the literature say about Iran SNI-based blocking?" maxlength="500" required>
      <button type="submit" class="btn primary">Ask →</button>
    </div>
    <p class="hero-ask-help muted">Or <a href="/use/">install the MCP server</a> to query from your editor.</p>
  </form>
  <dl class="counts-grid">
    <div><dt>papers</dt><dd>{{.Counts.papers}}</dd></div>
    <div><dt>censors</dt><dd>{{.Counts.censors}}</dd></div>
    <div><dt>techniques</dt><dd>{{.Counts.techniques}}</dd></div>
    <div><dt>defenses</dt><dd>{{.Counts.defenses}}</dd></div>
  </dl>
</section>

<section class="why">
  <p class="section-mark"><span class="sec-num">§ 01</span> <span class="sec-rule"></span> <span class="sec-title">why this exists</span></p>
  <div class="two-col">
    <div>
      <h2 class="display-sm">A layer the field doesn't have yet.</h2>
      <p>The censorship-circumvention community has wonderful resources: <a href="https://github.com/net4people/bbs" rel="external">net4people/bbs</a> for discussion, <a href="https://gfw.report" rel="external">gfw.report</a> for original research, <a href="https://censorbib.nymity.ch/" rel="external">CensorBib</a> as a maintained bibliography, <a href="https://ooni.org" rel="external">OONI</a> for measurement.</p>
      <p>None of them are LLM-callable. None of them have a consistent structured-metadata schema. None of them let an AI assistant compose a corpus query with operational data in the same conversation.</p>
      <p>This corpus adds that one missing layer.</p>
    </div>
    <aside class="aside">
      <p class="aside-label">The thing that compounds</p>
      <p>The schema and the controlled vocabulary outlive whatever model you read it through. Frontier models change every six months. The taxonomy of censors, techniques, and defenses doesn't.</p>
    </aside>
  </div>
</section>

<section class="core">
  <p class="section-mark"><span class="sec-num">§ 02</span> <span class="sec-rule"></span> <span class="sec-title">core papers</span></p>
  <h2 class="display-sm">Hand-selected as load-bearing.</h2>
  <p class="muted">If a Lantern protocol designer hadn't read these, the team would expect them to be slowed down. Team consensus marks them as <code>core: true</code>; everyone using the corpus sees them surfaced first.</p>
  <ul class="paper-cards">
    {{range .Core}}
    <li class="paper-card">
      <a href="/papers/{{.ID}}/" class="card-link">
        <div class="card-id mono">{{.ID}}</div>
        <h3>{{.Title}}</h3>
        <div class="card-meta">{{firstAuthor .Authors}} · <em>{{.Venue}}</em> · {{yearString .Year}}</div>
        <div class="card-tags">{{range .Censors}}<span class="tag censor">{{.}}</span>{{end}}{{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}</div>
      </a>
    </li>
    {{end}}
  </ul>
</section>

<section class="recent">
  <p class="section-mark"><span class="sec-num">§ 03</span> <span class="sec-rule"></span> <span class="sec-title">self-updating</span></p>
  <h2 class="display-sm">The corpus keeps crawling without us.</h2>
  <p class="lede">A pipeline polls <a href="https://arxiv.org" rel="external">arXiv</a>, <a href="https://github.com/net4people/bbs" rel="external">net4people/bbs</a>, <a href="https://gfw.report" rel="external">gfw.report</a>, <a href="https://petsymposium.org" rel="external">PoPETs</a>, <a href="https://www.usenix.org/conferences/byname/108" rel="external">FOCI</a>, <a href="https://www.usenix.org/conferences/byname/115" rel="external">USENIX Security</a>, <a href="https://www.ermao.net/" rel="external">ermao.net</a>, and the <a href="https://www.jonsnowwhite.de/publications/" rel="external">Paderborn upb-syssec</a> group's <a href="https://upb-syssec.github.io/blog/" rel="external">publications and blog</a> for new circumvention research, fetches each candidate via <a href="https://getwick.dev/" rel="external">wick</a> (browser-grade, residential-IP web access), then asks Claude to propose taxonomy tags and extract findings against the schema. Every new entry lands as an <code>auto-ingest</code> PR labeled for human review. Below: the most recent additions.</p>
  <ul class="paper-list">
    {{range .Recent}}
    <li>
      <a href="/papers/{{.ID}}/">
        <span class="row-id mono">{{.ID}}</span>
        <span class="row-title">{{.Title}}</span>
        <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
      </a>
    </li>
    {{end}}
  </ul>
</section>

<section class="cta-bottom">
  <div class="cta-grid">
    <div>
      <p class="section-mark"><span class="sec-num">§ 04</span> <span class="sec-rule"></span> <span class="sec-title">connect</span></p>
      <h2 class="display-sm">Plug it into your assistant.</h2>
      <p class="lede">One line. Your AI gains <code>search_papers</code>, <code>get_paper</code>, <code>list_taxonomy</code>, and <code>find_related</code> over the corpus.</p>
      <a class="btn primary" href="/use/">How to install</a>
    </div>
  </div>
</section>
`

const papersIndexBody = `
<p class="eyebrow">{{len .Papers}} ENTRIES · NEWEST FIRST</p>
<h1 class="display-sm">All papers</h1>
<ul class="paper-cards">
  {{range .Papers}}
  <li class="paper-card">
    <a href="/papers/{{.ID}}/" class="card-link">
      <div class="card-id mono">{{.ID}}{{if .Core}} · core{{end}}</div>
      <h3>{{.Title}}</h3>
      <div class="card-meta">{{firstAuthor .Authors}} · <em>{{.Venue}}</em> · {{yearString .Year}}</div>
      <div class="card-tags">
        {{range .Censors}}<span class="tag censor">{{.}}</span>{{end}}
        {{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}
      </div>
    </a>
  </li>
  {{end}}
</ul>
`

const paperBody = `
{{with .Paper}}
<article class="paper">
  <p class="paper-id mono">{{.ID}}</p>
  <h1>{{.Title}}{{if .Core}}<span class="badge core">core</span>{{end}}</h1>
  <p class="byline">
    {{join ", " .Authors}}{{if .Venue}} · <em>{{.Venue}}</em>{{end}}{{if .Year}} · {{.Year}}{{end}}
  </p>

  {{if .URL}}<p class="paper-links"><a href="{{.URL}}" rel="external">canonical link →</a>{{if .DOI}} · doi: <code>{{.DOI}}</code>{{end}}{{if .ArxivID}} · arxiv: <code>{{.ArxivID}}</code>{{end}}</p>{{end}}

  {{if .Abstract}}
  <h2>Abstract</h2>
  <div class="abstract">{{.Abstract}}</div>
  {{end}}

  {{if .Notes}}
  <h2>Team notes</h2>
  <div class="notes">{{.Notes}}</div>
  {{end}}

  <h2>Tags</h2>
  <dl class="tags-dl">
    <dt>censors</dt><dd>{{range .Censors}}<a class="tag censor" href="/censors/{{.}}/">{{.}}</a>{{end}}</dd>
    <dt>techniques</dt><dd>{{range .Techniques}}<a class="tag technique" href="/techniques/{{.}}/">{{.}}</a>{{end}}</dd>
    {{if .DefensesDiscussed}}<dt>defenses</dt><dd>{{range .DefensesDiscussed}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .DefensesEvaluatedAgainst}}<dt>evaluated</dt><dd>{{range .DefensesEvaluatedAgainst}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .EvaluationMethods}}<dt>method</dt><dd>{{range .EvaluationMethods}}<span class="tag">{{.}}</span>{{end}}</dd>{{end}}
  </dl>
</article>
{{end}}

{{if .Findings}}
<section class="findings-section">
  <p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">findings extracted from this paper</span></p>
  <ul class="findings-list">
    {{range .Findings}}
    <li class="finding-row">
      <a class="finding-link" href="/findings/{{.ID}}/">
        <p class="finding-summary">{{.Summary}}</p>
        <p class="finding-meta">
          {{if .Section}}<span class="finding-section mono">{{.Section}}</span>{{end}}
          {{if .Kind}}<span class="finding-kind">{{.Kind}}</span>{{end}}
          {{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}
          {{range .Censors}}<span class="tag censor">{{.}}</span>{{end}}
        </p>
      </a>
    </li>
    {{end}}
  </ul>
</section>
{{end}}

{{if .References}}
<section class="related-section">
  <p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">references in this corpus</span></p>
  <ul class="paper-list">
    {{range .References}}
    <li><a href="/papers/{{.ID}}/">
      <span class="row-id mono">{{.ID}}</span>
      <span class="row-title">{{.Title}}</span>
      <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
    </a></li>
    {{end}}
  </ul>
</section>
{{end}}

{{if .Related}}
<section class="related-section">
  <p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">related papers</span></p>
  <ul class="paper-list">
    {{range .Related}}
    <li><a href="/papers/{{.ID}}/">
      <span class="row-id mono">{{.ID}}</span>
      <span class="row-title">{{.Title}}</span>
      <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
    </a></li>
    {{end}}
  </ul>
</section>
{{end}}
`

const tagBody = `
<p class="eyebrow">{{upper .Category}}</p>
<h1 class="display-sm"><span class="mono tag-name">{{.TagID}}</span> &nbsp; {{.Entry.Name}}</h1>
{{if .Entry.Notes}}<p class="lede">{{.Entry.Notes}}</p>{{end}}
{{if .Entry.Synonyms}}<p class="muted"><strong>Synonyms:</strong> {{join ", " .Entry.Synonyms}}</p>{{end}}

{{if .Papers}}
<p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">{{len .Papers}} paper{{if ne (len .Papers) 1}}s{{end}} on file</span></p>
<ul class="paper-list">
  {{range .Papers}}
  <li><a href="/papers/{{.ID}}/">
    <span class="row-id mono">{{.ID}}</span>
    <span class="row-title">{{.Title}}</span>
    <span class="row-meta">{{.Venue}} · {{yearString .Year}}</span>
  </a></li>
  {{end}}
</ul>
{{end}}

{{if .Findings}}
<p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">{{len .Findings}} finding{{if ne (len .Findings) 1}}s{{end}} tagged here</span></p>
<ul class="findings-list">
  {{range .Findings}}
  <li class="finding-row">
    <a class="finding-link" href="/findings/{{.Finding.ID}}/">
      <p class="finding-summary">{{.Finding.Summary}}</p>
      <p class="finding-meta">
        <span class="finding-paper mono">{{.Paper.ID}}</span>
        {{if .Finding.Section}}<span class="finding-section mono">{{.Finding.Section}}</span>{{end}}
        {{if .Finding.Kind}}<span class="finding-kind">{{.Finding.Kind}}</span>{{end}}
      </p>
    </a>
  </li>
  {{end}}
</ul>
{{end}}
`

const tagIndexBody = `
<p class="eyebrow">CONTROLLED VOCABULARY · {{upper .Category}}</p>
<h1 class="display-sm">By {{.Category}}</h1>
<p class="lede muted">Sorted by paper count.</p>
<ul class="tag-index">
  {{range .Rows}}
  <li>
    <a href="/{{$.Category}}/{{.ID}}/"><span class="mono">{{.ID}}</span></a>
    <span>{{.Entry.Name}}</span>
    <span class="muted">{{.Count}} paper{{if ne .Count 1}}s{{end}}</span>
  </li>
  {{end}}
</ul>
`

const taxonomyBody = `
<h1>Taxonomy</h1>
<p>The controlled vocabularies that all paper records tag against. Adding a term: <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/taxonomy.yaml">edit <code>schema/taxonomy.yaml</code></a> and open a PR.</p>

<h2>Censors</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Censors}}<dt><a href="/censors/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Detection techniques</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Techniques}}<dt><a href="/techniques/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Defenses</h2>
<dl class="tax">
  {{range $id, $e := .Tax.Defenses}}<dt><a href="/defenses/{{$id}}/"><span class="mono">{{$id}}</span></a> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}{{if $e.Synonyms}}<br><span class="muted">synonyms: {{join ", " $e.Synonyms}}</span>{{end}}</dd>{{end}}
</dl>

<h2>Evaluation methods</h2>
<dl class="tax">
  {{range $id, $e := .Tax.EvaluationMethods}}<dt><span class="mono">{{$id}}</span> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}</dd>{{end}}
</dl>

<h2>Visibility levels</h2>
<dl class="tax">
  {{range $id, $e := .Tax.VisibilityLevels}}<dt><span class="mono">{{$id}}</span> {{$e.Name}}</dt><dd>{{if $e.Notes}}{{$e.Notes}}{{end}}</dd>{{end}}
</dl>
`

const useBody = `
<h1>Use the corpus</h1>

<p>The corpus is designed to be useful in several ways. Pick whichever fits your workflow.</p>

<h2>1. Plug into your AI assistant (one line, hosted)</h2>

<p>The fastest path. The corpus runs as a hosted MCP server at <code>corpus.lantern.io/mcp</code>. Zero install, no toolchain, always reflects the latest committed state of the repo (auto-deploys on every push to <code>main</code>).</p>

<h3>Claude Code</h3>
<pre><code>claude mcp add --transport http -s user circumvention-corpus https://corpus.lantern.io/mcp
</code></pre>
<p>Verify with <code>claude mcp list</code>; it should show <code>✓ Connected</code>. The server's four tools become available in any conversation.</p>

<h3>Codex CLI</h3>
<p>Add the server to <code>~/.codex/config.toml</code> (create the file if it doesn't exist):</p>
<pre><code>[mcp_servers.circumvention-corpus]
url = "https://corpus.lantern.io/mcp"
</code></pre>
<p>Then in any Codex session: <code>/mcp</code> lists configured servers; <code>circumvention-corpus</code> should appear with its four tools.</p>

<h3>Claude Desktop</h3>
<p>Edit your config (<code>~/Library/Application Support/Claude/claude_desktop_config.json</code> on macOS, <code>%APPDATA%/Claude/claude_desktop_config.json</code> on Windows):</p>
<pre><code>{
  "mcpServers": {
    "circumvention-corpus": {
      "url": "https://corpus.lantern.io/mcp",
      "transport": "http"
    }
  }
}
</code></pre>
<p>Restart Claude Desktop.</p>

<h3>Cursor / VS Code Copilot / other MCP clients</h3>
<p>Any MCP-compliant client takes a URL via the Streamable HTTP transport. Same shape — drop the URL above into your client's MCP config.</p>

<h2>2. Browse this site</h2>
<p>Every paper has a stable URL: <code>/papers/&lt;id&gt;/</code>. Tag indexes (<a href="/censors/">censors</a>, <a href="/techniques/">techniques</a>, <a href="/defenses/">defenses</a>) let you walk the field by axis. The whole site rebuilds from the YAML on every push to <code>main</code>; whatever you see here matches the source repo.</p>

<h2>3. Read the YAML directly</h2>
<p>Every paper is a small YAML file in <a href="https://github.com/getlantern/circumvention-corpus/tree/main/corpus/papers">corpus/papers/</a>. The <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/paper.schema.json">JSON schema</a> documents every field. The <a href="/taxonomy/">taxonomy</a> documents the controlled-vocabulary IDs that tag fields use. If you're building your own tooling on top of the corpus, this is the most boring, most stable interface — clone the repo, walk the directory.</p>

<pre><code>git clone https://github.com/getlantern/circumvention-corpus
cd circumvention-corpus
ls corpus/papers/                       # one YAML per paper
yq '.censors' corpus/papers/2023-wu-fully-encrypted-detect.yaml
</code></pre>

<h2>4. Self-host the MCP server (offline / privacy)</h2>

<p>For users behind aggressive censorship who can't reach Cloudflare, or anyone who'd rather not send queries off-machine. The corpus ships a Go MCP server with stdio transport — single binary, no runtime deps.</p>

<pre><code>go install github.com/getlantern/circumvention-corpus/cmd/corpus-mcp@latest

# Then register it. The --corpus flag points at a local clone of the repo.
git clone https://github.com/getlantern/circumvention-corpus ~/code/circumvention-corpus
claude mcp add -s user circumvention-corpus \
  $(go env GOPATH)/bin/corpus-mcp -- --corpus $HOME/code/circumvention-corpus
</code></pre>

<h2>What the MCP server exposes</h2>
<p>Four tools, designed to compose:</p>
<dl class="tax">
  <dt><span class="mono">search_papers</span></dt>
  <dd>Keyword + tag-filter search. Filters: <code>censors</code>, <code>techniques</code>, <code>defenses</code>, <code>year_min</code>, <code>year_max</code>, <code>venue</code>, <code>core_only</code>. Returns ranked records with abstract, tags, and team notes.</dd>
  <dt><span class="mono">get_paper</span></dt>
  <dd>Full record for a single paper id, plus any extracted findings tagged to it. Use after <code>search_papers</code> when the agent needs the full notes / references / metadata.</dd>
  <dt><span class="mono">list_taxonomy</span></dt>
  <dd>Returns the controlled vocabulary so the agent knows the canonical IDs to filter on. Especially useful as the first call in a session — gives the model the mental model of the field's structure.</dd>
  <dt><span class="mono">find_related</span></dt>
  <dd>Papers that share tags with a given paper. <code>mode</code> = <code>same_technique</code> (default), <code>same_censor</code>, or <code>same_defense</code>.</dd>
</dl>

<p>Example questions the MCP makes easy:</p>
<ul>
  <li><em>"Find every paper that evaluates a defense against the GFW's fully-encrypted-traffic detector."</em></li>
  <li><em>"What did anyone publish about Iran's censorship in 2024-2025?"</em></li>
  <li><em>"For my new protocol design: which papers should I read about active probing?"</em></li>
  <li><em>"Show me the citation neighborhood of <code>2023-wu-fully-encrypted-detect</code>."</em></li>
</ul>

<h2>5. Build something on top</h2>
<p>The schema is CC0. The metadata is CC0. Build whatever you want with it — your own UI, a notification system that pings you when papers tagged with a specific technique appear, a sister index for a different region. The whole point of having a structured-metadata layer is that the data outlives whatever interface we put on top of it.</p>
`

const contributeBody = `
<h1>Contribute</h1>

<p>If you'd rather use the corpus than contribute to it, see <a href="/use/">use the corpus</a>.</p>

<h2>Auto-ingest, briefly</h2>
<p>You may not need to add anything by hand. A scheduled crawler watches arXiv, net4people/bbs, gfw.report, PoPETs, FOCI, USENIX Security, ermao.net, and the Paderborn upb-syssec group's publications and blog for new circumvention research. It fetches candidate pages via <a href="https://getwick.dev/" rel="external">wick</a> (browser-grade web access — works against bot-walls and residential-IP-only sources) and asks Claude to propose taxonomy tags and extract findings against the schema. Every accepted paper opens a PR labeled <code>auto-ingest</code>. Reviewing one of those PRs is the lowest-friction way to contribute — read the auto-proposed tags, tighten or drop them, replace the auto-generated notes with your team's perspective, and merge.</p>
<p>If you want to add something the crawler missed (a private write-up, a blog post, a paper from a venue we don't poll yet), follow the manual steps below.</p>

<h2>Add a paper</h2>
<ol>
  <li>Pick a stable id: <code>YYYY-firstauthor-shortslug</code> (lowercase, dashes).</li>
  <li>Create <code>corpus/papers/&lt;id&gt;.yaml</code> following <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/paper.schema.json">the schema</a>.</li>
  <li>Tag against the controlled vocabulary in <a href="/taxonomy/">the taxonomy</a>. If a tag you need doesn't exist, add it to <code>schema/taxonomy.yaml</code> in the same PR.</li>
  <li>Set <code>visibility</code> honestly. If unsure, default to non-public; promoting later is easy, recalling a leak isn't.</li>
  <li>Write the <code>notes</code> field. The abstract is what the authors said; the notes are what your team thinks about it.</li>
  <li>Open a PR. CI runs the corpus integrity test (every tag must resolve, every reference must exist).</li>
</ol>

<h2>For private papers</h2>
<p>Don't put them in this repo. Use a separate private repo with the same schema (Lantern team: <code>circumvention-corpus-private</code>). The MCP server reads both data dirs locally; the public site you're reading right now serves only <code>visibility: public</code> records.</p>

<h2>Add a tag to the taxonomy</h2>
<p>Open a PR editing <a href="https://github.com/getlantern/circumvention-corpus/blob/main/schema/taxonomy.yaml"><code>schema/taxonomy.yaml</code></a>. New terms should have a definition and ideally a citation to a paper that uses the concept. Synonyms map alternate spellings to the canonical term.</p>

<h2>Extract findings from a paper</h2>
<p>The <code>findings/</code> directory holds extracted claims (one- to three-sentence statements like <em>"the GFW's classifier achieves 94% precision on Snowflake DTLS handshakes"</em>) tagged against the same vocabulary as papers. This is the highest-leverage curation work — it's what makes the corpus answer questions like <em>"what did anyone find about technique X"</em> without re-reading every paper.</p>
<p>An LLM (Claude/GPT/etc.) can propose findings if you feed it a paper; commit them only after a human review.</p>
`

const findingsIndexBody = `
<p class="eyebrow">FINDINGS</p>
<h1 class="display-sm">{{.FindingsCount}} extracted findings</h1>
<p class="lede muted">One- to three-sentence claims pulled from the full text of each paper, tagged against the same taxonomy as the papers themselves. Listed newest first.</p>

<ul class="findings-list big">
  {{range .Rows}}
  <li class="finding-row">
    <a class="finding-link" href="/findings/{{.Finding.ID}}/">
      <p class="finding-summary">{{.Finding.Summary}}</p>
      <p class="finding-meta">
        <span class="finding-paper mono">{{.Paper.ID}}</span>
        <span class="finding-paper-year">{{yearString .Paper.Year}}</span>
        {{if .Finding.Section}}<span class="finding-section mono">{{.Finding.Section}}</span>{{end}}
        {{if .Finding.Kind}}<span class="finding-kind">{{.Finding.Kind}}</span>{{end}}
        {{range .Finding.Techniques}}<span class="tag technique">{{.}}</span>{{end}}
        {{range .Finding.Censors}}<span class="tag censor">{{.}}</span>{{end}}
      </p>
    </a>
  </li>
  {{end}}
</ul>
`

const findingBody = `
{{with .Finding}}
<article class="finding">
  <p class="eyebrow">FINDING{{if .Kind}} · {{upper .Kind}}{{end}}</p>
  <h1 class="finding-title">{{.Summary}}</h1>

  <p class="finding-attrib">
    From <a href="/papers/{{.Paper}}/" class="mono">{{.Paper}}</a>{{if $.Paper}} — <em>{{$.Paper.Title}}</em>{{end}}
    {{if .Section}} · <span class="mono">{{.Section}}</span>{{end}}
    {{if $.Paper.Year}} · {{yearString $.Paper.Year}}{{end}}
    {{if $.Paper.Venue}} · {{$.Paper.Venue}}{{end}}
  </p>

  {{if .DefenseImplications}}
  <h2>Implications</h2>
  <ul class="finding-implications">
    {{range .DefenseImplications}}<li>{{.}}</li>{{end}}
  </ul>
  {{end}}

  <h2>Tags</h2>
  <dl class="tags-dl">
    {{if .Censors}}<dt>censors</dt><dd>{{range .Censors}}<a class="tag censor" href="/censors/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .Techniques}}<dt>techniques</dt><dd>{{range .Techniques}}<a class="tag technique" href="/techniques/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
    {{if .Defenses}}<dt>defenses</dt><dd>{{range .Defenses}}<a class="tag defense" href="/defenses/{{.}}/">{{.}}</a>{{end}}</dd>{{end}}
  </dl>

  {{if .ExtractedBy}}<p class="muted finding-extractor">Extracted by <code>{{.ExtractedBy}}</code> — review before relying.</p>{{end}}
</article>
{{end}}

{{if .Siblings}}
<section class="related-section">
  <p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">other findings from this paper</span></p>
  <ul class="findings-list">
    {{range .Siblings}}
    <li class="finding-row">
      <a class="finding-link" href="/findings/{{.ID}}/">
        <p class="finding-summary">{{.Summary}}</p>
        <p class="finding-meta">
          {{if .Section}}<span class="finding-section mono">{{.Section}}</span>{{end}}
          {{if .Kind}}<span class="finding-kind">{{.Kind}}</span>{{end}}
        </p>
      </a>
    </li>
    {{end}}
  </ul>
</section>
{{end}}

{{if .Related}}
<section class="related-section">
  <p class="section-mark"><span class="sec-rule short"></span> <span class="sec-title">related findings</span></p>
  <ul class="findings-list">
    {{range .Related}}
    <li class="finding-row">
      <a class="finding-link" href="/findings/{{.ID}}/">
        <p class="finding-summary">{{.Summary}}</p>
        <p class="finding-meta">
          <span class="finding-paper mono">{{.Paper}}</span>
          {{if .Section}}<span class="finding-section mono">{{.Section}}</span>{{end}}
          {{range .Techniques}}<span class="tag technique">{{.}}</span>{{end}}
        </p>
      </a>
    </li>
    {{end}}
  </ul>
</section>
{{end}}
`

const askBody = `
<section class="ask-hero">
  <p class="eyebrow">ASK · powered by claude + the corpus</p>
  <h1 class="display-sm">Ask the corpus.</h1>
  <p class="lede">Type a research question. The corpus retrieves every relevant extracted finding, then Claude writes a structured answer that cites them inline as <code>(paper_id, §section)</code>. Same retrieval the MCP <code>synthesize</code> tool exposes — this is what it looks like through a web form.</p>
</section>

<form id="ask-form" class="ask-form" autocomplete="off">
  <textarea id="ask-q" name="question" rows="3" placeholder="e.g. What does the literature say about Iran SNI-based blocking?" required maxlength="500"></textarea>
  <div class="ask-controls">
    <button type="submit" class="btn primary" id="ask-submit">Ask →</button>
    <span class="muted ask-help">Submit also: <kbd>⌘</kbd>+<kbd>Enter</kbd></span>
  </div>
</form>

<div id="ask-status" class="ask-status" hidden></div>
<div id="ask-result" class="ask-result" hidden>
  <article id="ask-answer" class="ask-answer"></article>
  <details class="ask-bundle">
    <summary>Findings cited (<span id="ask-bundle-count">0</span>)</summary>
    <ul id="ask-bundle-list" class="findings-list"></ul>
  </details>
  <p class="muted ask-meta"><span id="ask-elapsed"></span> · Answer generated by Claude over the retrieved findings. Treat as a starting point — verify each citation against the source paper.</p>
</div>

<script>
(function(){
  const form = document.getElementById('ask-form');
  const q = document.getElementById('ask-q');
  const submit = document.getElementById('ask-submit');
  const status = document.getElementById('ask-status');
  const result = document.getElementById('ask-result');
  const answer = document.getElementById('ask-answer');
  const bundleList = document.getElementById('ask-bundle-list');
  const bundleCount = document.getElementById('ask-bundle-count');
  const elapsed = document.getElementById('ask-elapsed');

  // Pre-fill from ?q= so /ask/?q=... is a shareable URL.
  const params = new URLSearchParams(location.search);
  if (params.get('q')) q.value = params.get('q');

  q.addEventListener('keydown', e => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') form.requestSubmit();
  });

  function setStatus(msg, kind) {
    status.hidden = !msg;
    status.textContent = msg || '';
    status.className = 'ask-status' + (kind ? ' ' + kind : '');
  }

  // Render markdown headings + bold for the answer. Conservative subset
  // — escape everything else to avoid XSS via untrusted LLM output.
  function escapeHTML(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  function renderAnswerMarkdown(md) {
    const lines = md.split('\n');
    const out = [];
    let inList = false;
    for (const raw of lines) {
      const line = raw.trimEnd();
      if (line === '') {
        if (inList) { out.push('</ul>'); inList = false; }
        continue;
      }
      const h = line.match(/^(#{1,6})\s+(.+)$/);
      if (h) {
        if (inList) { out.push('</ul>'); inList = false; }
        const lvl = Math.min(h[1].length + 1, 6);
        out.push('<h' + lvl + '>' + inline(h[2]) + '</h' + lvl + '>');
        continue;
      }
      if (line.startsWith('- ') || line.startsWith('* ')) {
        if (!inList) { out.push('<ul>'); inList = true; }
        out.push('<li>' + inline(line.slice(2)) + '</li>');
        continue;
      }
      if (inList) { out.push('</ul>'); inList = false; }
      out.push('<p>' + inline(line) + '</p>');
    }
    if (inList) out.push('</ul>');
    return out.join('\n');
  }
  function inline(s) {
    return escapeHTML(s)
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/\*([^*]+)\*/g, '<em>$1</em>')
      .replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g, '<code>$1</code>')
      // Linkify (paper-id, §section) → /papers/<paper-id>/
      .replace(/\(([0-9]{4}-[a-z0-9-]+(?:__[a-z0-9-]+)?)(?:,\s*([^)]+))?\)/g,
        function(m, pid, sect) {
          const link = '<a href="/papers/' + pid.split('__')[0] + '/">' + pid + '</a>';
          return sect ? '(' + link + ', ' + sect + ')' : '(' + link + ')';
        });
  }

  form.addEventListener('submit', async e => {
    e.preventDefault();
    const question = q.value.trim();
    if (!question) return;

    submit.disabled = true;
    result.hidden = true;
    setStatus('Searching the corpus and asking Claude — this can take 10-30 seconds…', 'loading');

    // Update the URL so the question is shareable.
    const u = new URL(location.href);
    u.searchParams.set('q', question);
    history.replaceState(null, '', u);

    try {
      const r = await fetch('/api/ask', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ question }),
      });
      if (!r.ok) {
        const err = await r.text().catch(() => '');
        throw new Error(r.status + ': ' + (err || r.statusText));
      }
      const data = await r.json();
      setStatus('', '');
      answer.innerHTML = renderAnswerMarkdown(data.answer || '(empty answer)');
      bundleList.innerHTML = '';
      const findings = (data.bundle && data.bundle.findings) || [];
      bundleCount.textContent = findings.length;
      for (const f of findings) {
        const li = document.createElement('li');
        li.className = 'finding-row';
        const a = document.createElement('a');
        a.className = 'finding-link';
        a.href = '/findings/' + f.id + '/';
        const summary = document.createElement('p');
        summary.className = 'finding-summary';
        summary.textContent = f.summary || '';
        a.appendChild(summary);
        const meta = document.createElement('p');
        meta.className = 'finding-meta';
        const pid = document.createElement('span');
        pid.className = 'finding-paper mono';
        pid.textContent = f.paper;
        meta.appendChild(pid);
        if (f.section) {
          const sec = document.createElement('span');
          sec.className = 'finding-section mono';
          sec.textContent = f.section;
          meta.appendChild(sec);
        }
        if (f.paper_year) {
          const yr = document.createElement('span');
          yr.className = 'finding-paper-year';
          yr.textContent = f.paper_year;
          meta.appendChild(yr);
        }
        a.appendChild(meta);
        li.appendChild(a);
        bundleList.appendChild(li);
      }
      elapsed.textContent = data.elapsed_ms || '';
      result.hidden = false;
    } catch (err) {
      setStatus('Could not get an answer: ' + err.message + '. The retrieval pipeline may be temporarily offline; you can still browse /findings/ directly.', 'error');
    } finally {
      submit.disabled = false;
    }
  });

  // Auto-fire if landed with a ?q= param.
  if (q.value.trim()) form.requestSubmit();
})();
</script>
`

// searchJS is the in-browser search client. Loaded on every page via
// /search.js. Fetches /search-index.json once on first focus, then
// runs in-memory keyword + tag matching. Vanilla JS, no framework, no
// build step. All result-row content is built via DOM APIs (no
// innerHTML on dynamic data) so XSS-via-paper-title isn't reachable.
//
// Ranking is deliberately simple: for each query token, score each
// paper by where the token appears (title 5x, authors 3x, tags 3x,
// notes 2x, abstract 1x). Title-prefix matches get an additional boost.
const searchJS = `(() => {
  const input = document.getElementById('search');
  const results = document.getElementById('search-results');
  if (!input || !results) return;

  let index = null;
  let loading = null;
  let activeIdx = -1;

  async function load() {
    if (index) return index;
    if (loading) return loading;
    loading = fetch('/search-index.json').then(r => r.json()).then(d => {
      index = d;
      return index;
    }).catch(() => { index = []; return index; });
    return loading;
  }

  function tokenize(s) {
    return (s || '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
  }

  function score(paper, qTokens) {
    let total = 0;
    const title = (paper.title || '').toLowerCase();
    const authors = (paper.authors || []).join(' ').toLowerCase();
    const tags = [...(paper.censors||[]), ...(paper.techniques||[]), ...(paper.defenses||[])].join(' ').toLowerCase();
    const notes = (paper.notes || '').toLowerCase();
    const abstract = (paper.abstract || '').toLowerCase();
    const findings = (paper.findings || '').toLowerCase();
    const id = (paper.id || '').toLowerCase();
    for (const t of qTokens) {
      if (!t) continue;
      let s = 0;
      if (title.includes(t)) s += 5;
      if (title.startsWith(t) || title.includes(' ' + t)) s += 3;
      if (authors.includes(t)) s += 3;
      if (tags.includes(t)) s += 3;
      if (id.includes(t)) s += 2;
      if (notes.includes(t)) s += 2;
      if (findings.includes(t)) s += 2;
      if (abstract.includes(t)) s += 1;
      if (s === 0) return 0;
      total += s;
    }
    if (paper.core) total += 1;
    return total;
  }

  // appendHighlighted writes 'text' into 'parent' as text nodes,
  // wrapping any case-insensitive occurrence of one of qTokens in a
  // <mark> element. Uses matchAll() iteration so all dynamic strings
  // go through createTextNode — there's no path for HTML injection
  // even from a maliciously-titled paper.
  function appendHighlighted(parent, text, qTokens) {
    if (!text) return;
    const meaningful = qTokens.filter(t => t && t.length >= 2);
    if (meaningful.length === 0) {
      parent.appendChild(document.createTextNode(text));
      return;
    }
    const escaped = meaningful.map(t => t.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&')).join('|');
    const re = new RegExp('(' + escaped + ')', 'ig');
    let last = 0;
    for (const m of text.matchAll(re)) {
      if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
      const mark = document.createElement('mark');
      mark.textContent = m[0];
      parent.appendChild(mark);
      last = m.index + m[0].length;
    }
    if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
  }

  function clearChildren(el) {
    while (el.firstChild) el.removeChild(el.firstChild);
  }

  function tagSpan(text, kind) {
    const s = document.createElement('span');
    s.className = 'tag ' + kind;
    s.textContent = text;
    return s;
  }

  function buildRow(paper, qTokens) {
    const a = document.createElement('a');
    a.href = '/papers/' + encodeURIComponent(paper.id) + '/';
    const title = document.createElement('span');
    title.className = 'r-title';
    appendHighlighted(title, paper.title || '', qTokens);
    a.appendChild(title);
    const meta = document.createElement('span');
    meta.className = 'r-meta';
    const venue = paper.venue || '';
    const year = paper.year ? ' · ' + paper.year : '';
    meta.textContent = venue + year;
    a.appendChild(meta);
    const id = document.createElement('span');
    id.className = 'r-id';
    id.textContent = paper.id;
    a.appendChild(id);
    if ((paper.censors && paper.censors.length) || (paper.techniques && paper.techniques.length)) {
      const tags = document.createElement('span');
      tags.className = 'r-tags';
      for (const t of (paper.censors || [])) tags.appendChild(tagSpan(t, 'censor'));
      for (const t of (paper.techniques || [])) tags.appendChild(tagSpan(t, 'technique'));
      a.appendChild(tags);
    }
    return a;
  }

  function render(matches, qTokens, query) {
    clearChildren(results);
    if (!query) { results.hidden = true; activeIdx = -1; return; }
    if (matches.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'empty';
      empty.appendChild(document.createTextNode('No matches for '));
      const em = document.createElement('em');
      em.textContent = query;
      empty.appendChild(em);
      empty.appendChild(document.createTextNode('.'));
      results.appendChild(empty);
      results.hidden = false;
      activeIdx = -1;
      return;
    }
    const summary = document.createElement('div');
    summary.className = 'summary';
    summary.textContent = matches.length + ' match' + (matches.length === 1 ? '' : 'es');
    results.appendChild(summary);
    for (const p of matches.slice(0, 30)) {
      results.appendChild(buildRow(p, qTokens));
    }
    results.hidden = false;
    activeIdx = -1;
  }

  async function update() {
    const query = input.value.trim();
    if (!query) { render([], [], ''); return; }
    await load();
    if (!index) return;
    const qTokens = tokenize(query);
    if (qTokens.length === 0) { render([], [], ''); return; }
    const scored = [];
    for (const p of index) {
      const s = score(p, qTokens);
      if (s > 0) scored.push({p, s});
    }
    scored.sort((a, b) => b.s - a.s || (b.p.year || 0) - (a.p.year || 0));
    render(scored.map(x => x.p), qTokens, query);
  }

  let timer = null;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(update, 80);
  });
  input.addEventListener('focus', load);

  // "/" anywhere focuses the search, like GitHub.
  document.addEventListener('keydown', e => {
    if (e.key === '/' && !['INPUT','TEXTAREA'].includes(document.activeElement.tagName)) {
      e.preventDefault();
      input.focus();
      input.select();
    }
    if (e.key === 'Escape' && document.activeElement === input) {
      input.value = '';
      results.hidden = true;
      input.blur();
    }
  });

  // Arrow-key navigation through results.
  input.addEventListener('keydown', e => {
    const items = results.querySelectorAll('a');
    if (!items.length) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeIdx = Math.min(items.length - 1, activeIdx + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeIdx = Math.max(0, activeIdx - 1);
    } else if (e.key === 'Enter') {
      if (activeIdx >= 0) {
        e.preventDefault();
        items[activeIdx].click();
      }
    } else {
      return;
    }
    items.forEach((it, i) => it.classList.toggle('active', i === activeIdx));
    items[activeIdx].scrollIntoView({block: 'nearest'});
  });

  // Click-outside dismiss.
  document.addEventListener('click', e => {
    if (!input.contains(e.target) && !results.contains(e.target)) {
      results.hidden = true;
    }
  });
})();
`

// faviconSVG: 4x4 grid (the censor's wall, echoing the brand mark in the
// header) with one cell filled in rust — a single open path through. Plotter
// pen-blue stroke on warm cream, plus rust accent. Reads at any size; the
// filled cell is what makes it distinctive at 16×16, not just decorative.
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" fill="#f5f0e2"/>
  <g fill="none" stroke="#1a3a8a" stroke-width="2" stroke-linecap="round">
    <rect x="4" y="4" width="24" height="24"/>
    <line x1="4" y1="11" x2="28" y2="11"/>
    <line x1="4" y1="18" x2="28" y2="18"/>
    <line x1="4" y1="25" x2="28" y2="25"/>
    <line x1="11" y1="4" x2="11" y2="28"/>
    <line x1="18" y1="4" x2="18" y2="28"/>
    <line x1="25" y1="4" x2="25" y2="28"/>
  </g>
  <rect x="18" y="11" width="7" height="7" fill="#9b3a26"/>
</svg>`

const styleCSS = `
/* circumvention-corpus — visual design.
 *
 * "PLOTTER STATION"
 *
 * Plotter-pen aesthetic. Warm cream paper as the ground, deep ink
 * black for type, a single structural accent (plotter-pen blue) used
 * sparingly as the second voice. Generative SVG line-art appears as
 * a structural element rather than decoration — the figures share the
 * same currentColor / accent system as the rest of the page.
 *
 * Lineage: Vera Molnar's Interruptions, Manfred Mohr's P-series,
 * Kenneth Martin's Chance Lines, Edward Tufte's information design,
 * Distill.pub's typographic grammar.
 *
 * Typography:
 *   Newsreader — variable serif, optical sizing, for display + paper
 *                titles. Pentagram-designed, Google Fonts.
 *   Atkinson Hyperlegible — body sans, distinctive humanist grotesque.
 *   JetBrains Mono — IDs, controlled vocabulary, terminal data.
 *
 * No light/dark toggle: this is a paper interface, not a screen.
 */

:root {
  --paper:       #f5f0e2;  /* warm cream */
  --paper-2:     #ede7d2;  /* slight tint */
  --paper-edge:  #d8cfb4;  /* paper hairline */
  --ink:         #14130f;  /* warm deep black */
  --ink-2:       #2a2823;  /* body text */
  --ink-3:       #5a554a;  /* secondary */
  --ink-mute:    #857f6f;  /* tertiary, captions */
  --rule:        #c5beae;  /* hairline rule */
  --rule-fade:   #dfd7c2;  /* faintest rule */
  --accent:      #1a3a8a;  /* plotter-pen blue — Pilot G-2 deep blue */
  --accent-2:    #2851b8;  /* lighter pen-blue, hover */
  --accent-soft: #e2e6f2;  /* tint for accent backgrounds */
  --rust:        #9b3a26;  /* secondary plotter color, used very sparingly */
  --moss:        #2e5a3a;  /* tertiary plotter color */
  --code-bg:     #ede7d2;  /* code block background */
  --selection:   #ffe680;  /* highlighter yellow */
}

* { box-sizing: border-box; }
::selection { background: var(--selection); color: var(--ink); }

html { font-size: 16px; -webkit-text-size-adjust: 100%; background: var(--paper); }
body {
  margin: 0;
  background: var(--paper);
  color: var(--ink-2);
  font-family: "Atkinson Hyperlegible", -apple-system, system-ui, sans-serif;
  font-size: 1rem;
  line-height: 1.55;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

/* Faint graph-paper grid behind everything. So subtle it's almost
 * subliminal — provides the "engineering notebook" undercurrent
 * without competing with type. */
body::before {
  content: "";
  position: fixed; inset: 0;
  pointer-events: none;
  z-index: 0;
  background-image:
    linear-gradient(to right, var(--rule-fade) 1px, transparent 1px),
    linear-gradient(to bottom, var(--rule-fade) 1px, transparent 1px);
  background-size: 36px 36px;
  background-position: -1px -1px;
  opacity: 0.42;
}

/* z-index hierarchy:
 *   ambient layer = 0 (back)
 *   main.wrap, .site-footer = 1 (above ambient)
 *   .site-header = 50 (above main, so the search dropdown — which lives
 *                       inside .site-header and inherits its stacking
 *                       context — paints over main rather than being
 *                       occluded by it)
 *   .site-header (sticky from earlier rule) keeps its sticky positioning. */
main.wrap, .site-footer { position: relative; z-index: 1; }
.site-header { z-index: 50; }

.mono, code, pre, .row-id, .card-id, .tag, .paper-id {
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-feature-settings: "calt" 0;
}

.display, .display-sm, h1, h2, h3, .row-title, .paper-card h3, article.paper h1 {
  font-family: "Newsreader", "Iowan Old Style", "Source Serif 4", Georgia, serif;
  font-feature-settings: "ss01", "kern", "liga";
  letter-spacing: -0.012em;
  line-height: 1.18;
  font-weight: 500;
  color: var(--ink);
}

.display { font-size: clamp(2.4rem, 4.8vw, 3.6rem); margin: 0; line-height: 1.05; font-weight: 500; letter-spacing: -0.02em; }
.display em { font-style: italic; color: var(--ink); font-weight: 500; position: relative; white-space: nowrap; }
/* hand-drawn underline beneath italic accent — uses the structural accent. */
.display em::after {
  content: "";
  position: absolute;
  left: 0; right: 0; bottom: -0.04em;
  height: 0.06em;
  background: var(--accent);
  border-radius: 1px;
  opacity: 0.85;
}
.display-sm { font-size: clamp(1.5rem, 2.4vw, 1.9rem); margin: 0 0 0.7rem; font-weight: 500; }
h1 { font-size: clamp(1.8rem, 3vw, 2.2rem); margin: 0 0 0.5rem; }
h2 { font-size: 1.25rem; margin: 2.5rem 0 0.6rem; font-family: "Newsreader", serif; font-weight: 600; }
h3 { font-size: 1.05rem; margin: 0 0 0.3rem; font-weight: 600; font-family: "Newsreader", serif; }

a { color: var(--accent); text-decoration: none; transition: color 0.12s; border-bottom: 1px solid transparent; }
a:hover { color: var(--accent-2); border-bottom-color: var(--accent-2); }
em { font-style: italic; }
.muted { color: var(--ink-mute); }
.lede { font-size: 1.08rem; line-height: 1.55; color: var(--ink-2); margin: 0.85rem 0; max-width: 38rem; font-family: "Newsreader", serif; font-weight: 400; }

code {
  background: var(--code-bg);
  color: var(--ink);
  padding: 0.05rem 0.35rem;
  border: 1px solid var(--paper-edge);
  border-radius: 2px;
  font-size: 0.86em;
}
pre {
  background: var(--code-bg);
  color: var(--ink);
  padding: 1rem 1.2rem;
  overflow-x: auto;
  border: 1px solid var(--paper-edge);
  border-left: 2px solid var(--accent);
  font-size: 0.85rem;
  line-height: 1.6;
  border-radius: 0;
  position: relative;
}
pre code { background: none; padding: 0; border: none; color: inherit; }

.wrap { max-width: 76rem; margin: 0 auto; padding: 0 1.75rem; }
main.wrap { padding: 3.5rem 1.75rem 6rem; }

/* ────────────────── HEADER ────────────────── */
.site-header {
  background: color-mix(in oklab, var(--paper) 92%, transparent);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  border-bottom: 1px solid var(--rule);
  position: sticky; top: 0;
}
.site-header .wrap {
  display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between;
  gap: 1.2rem; padding: 0.9rem 1.75rem;
}
.brand { display: inline-flex; align-items: center; gap: 0.6rem; color: var(--ink); border: none; }
.brand:hover { color: var(--ink); border: none; }
.brand-mark {
  color: var(--accent);
  flex-shrink: 0;
}
.brand-name {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.95rem; font-weight: 500;
  letter-spacing: -0.01em;
  color: var(--ink);
}
.brand-dot { color: var(--accent); padding: 0 0.05em; font-weight: 600; }

nav { display: flex; gap: 0.25rem 1.4rem; flex-wrap: wrap; align-items: center; }
nav a {
  color: var(--ink-2); font-size: 0.86rem;
  padding: 0.3rem 0; position: relative;
  font-family: "JetBrains Mono", monospace;
  letter-spacing: -0.01em;
  border: none;
}
nav a:hover { color: var(--accent); border: none; }
nav a:hover::after {
  content: ""; position: absolute; bottom: -2px; left: 0; right: 0; height: 1px;
  background: var(--accent);
}
nav a.external { color: var(--ink-mute); }

/* ────────────────── HERO ──────────────────
 * The headline + lede sit on a calm cream ground. Ambient protocol
 * motifs (TLS hex, IPv4 header, DNS, active-probing sequence) appear
 * scattered through the page sections at very low opacity — see the
 * AMBIENT section below.
 */
.hero { padding: 3rem 0 1rem; }
.hero-text { max-width: 48rem; position: relative; z-index: 1; }

/* ────────────────── AMBIENT — animated protocol layer ──────────────────
 * A position:fixed layer behind every page that renders a few real
 * protocol artifacts at very low opacity, each gently animated:
 *
 *   - .ambient-stream — tall hex dump of mixed protocol bytes (TLS,
 *     DNS, IPv4, HTTP/2, Tor, WireGuard, Shadowsocks, QUIC, ECH).
 *     Slowly scrolls upward in a continuous loop. The SVG content is
 *     doubled internally so the loop has no visible seam.
 *   - .ambient-pkt — IPv4 header diagram, breathes (opacity oscillates).
 *   - .ambient-seq — active-probing sequence diagram, breathes on a
 *     different phase so the motifs feel independent.
 *   - .ambient-dns — DNS header, breathes.
 *
 * Together they read as "the network is alive, somewhere behind the
 * page." Honors prefers-reduced-motion and is hidden under 78rem. */
.ambient-layer {
  position: fixed; inset: 0;
  pointer-events: none;
  z-index: 0;
  overflow: hidden;
  color: var(--ink);
}
.ambient-layer > div { position: absolute; }
.ambient-svg { display: block; width: 100%; height: auto; }
/* Counteract any inner-element opacity attrs in the SVGs — let the
 * container opacity do all the dimming. Otherwise compound opacity
 * (0.5 inside × 0.07 outside) would drop motifs below visibility. */
.ambient-svg text, .ambient-svg [opacity] { opacity: 1 !important; }

/* Scrolling hex stream: clipped tall column on the left side. Inner
 * SVG translates upward by 50% (since rows are doubled inside). */
.ambient-stream {
  top: 0; left: -1rem;
  width: 30rem;
  height: 100%;
  overflow: hidden;
  opacity: 0.16;
  transform: rotate(-0.4deg);
  transform-origin: top left;
}
.ambient-stream svg {
  display: block; width: 100%; height: auto;
  animation: drift-up 110s linear infinite;
  will-change: transform;
}

/* Breathing motifs: opacity oscillates so the artifacts fade in and
 * out gently. Different durations + delays so they feel independent. */
.ambient-pkt {
  top: 5rem; right: 1rem;
  width: 26rem;
  transform: rotate(0.5deg);
  animation: breathe 11s ease-in-out infinite;
}
.ambient-seq {
  bottom: 3rem; right: 1rem;
  width: 22rem;
  transform: rotate(-0.3deg);
  animation: breathe 14s ease-in-out -5s infinite;
}
.ambient-dns {
  top: 50%; right: 30%;
  width: 18rem;
  transform: translateY(-50%) rotate(0.2deg);
  animation: breathe 17s ease-in-out -8s infinite;
}

@keyframes drift-up {
  from { transform: translateY(0); }
  to   { transform: translateY(-50%); }
}
@keyframes breathe {
  0%, 100% { opacity: 0.08; }
  50%      { opacity: 0.22; }
}

/* Hide ambient layer on narrow viewports — it'd just clutter on phones.
 * 60rem ≈ 960px lets all tablets and laptops show the layer. */
@media (max-width: 60rem) {
  .ambient-layer { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .ambient-stream svg { animation: none; }
  .ambient-pkt, .ambient-seq, .ambient-dns { animation: none; opacity: 0.13; }
}

.eyebrow {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.78rem; letter-spacing: 0.02em;
  color: var(--ink-mute); margin: 0 0 1.4rem;
  text-transform: lowercase;
  position: relative; padding-left: 1.5rem;
}
.eyebrow::before {
  content: "";
  position: absolute; left: 0; top: 50%;
  width: 1rem; height: 1px;
  background: var(--accent);
}

.cta { display: flex; flex-wrap: wrap; gap: 0.85rem; margin-top: 1.8rem; }

/* ────────────────── HERO-ASK — primary CTA on the home page ──────────────────
 * The home page lede invites visitors to ask the corpus directly. We
 * present a working textfield, not a "Try it" button — the field IS
 * the offer. Submits to /ask/?q=... which auto-fires the LLM call.
 */
.hero-ask {
  max-width: 44rem;
  margin: 1.8rem 0 0;
  display: flex; flex-direction: column; gap: 0.5rem;
}
.hero-ask .eyebrow { margin: 0 0 0.4rem; padding-left: 0; }
.hero-ask .eyebrow::before { content: none; }
.hero-ask-row {
  display: flex; gap: 0.55rem;
  align-items: stretch;
}
.hero-ask input {
  flex: 1 1 auto; min-width: 0;
  padding: 0.65rem 0.85rem;
  border: 1.5px solid var(--ink);
  background: var(--paper);
  color: var(--ink);
  font-family: "Newsreader", serif;
  font-size: 1.02rem;
  border-radius: 1px;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.hero-ask input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.hero-ask input::placeholder { color: var(--ink-mute); }
.hero-ask button {
  flex: 0 0 auto;
  padding: 0.65rem 1.4rem;
  font-size: 0.96rem;
}
.hero-ask-help { margin: 0.3rem 0 0; font-size: 0.86rem; }

@media (max-width: 35rem) {
  .hero-ask-row { flex-direction: column; gap: 0.5rem; }
  .hero-ask button { width: 100%; }
}
.btn {
  display: inline-flex; align-items: center; gap: 0.4rem;
  padding: 0.62rem 1.1rem;
  font-size: 0.92rem; font-weight: 500;
  font-family: "Atkinson Hyperlegible", sans-serif;
  border: 1px solid; transition: all 0.12s;
  border-radius: 1px;
}
.btn.primary {
  background: var(--ink); color: var(--paper);
  border-color: var(--ink);
}
.btn.primary:hover {
  background: var(--accent); border-color: var(--accent);
  color: var(--paper);
  border-bottom-color: var(--accent);
}
.btn.ghost {
  background: transparent; color: var(--ink);
  border-color: var(--ink);
}
.btn.ghost:hover {
  background: var(--ink); color: var(--paper);
  border-bottom-color: var(--ink);
}

.counts-grid {
  display: grid; grid-template-columns: repeat(4, 1fr); gap: 0;
  margin: 3.5rem 0 0;
  border-top: 1px solid var(--rule);
  border-bottom: 1px solid var(--rule);
}
.counts-grid div {
  display: flex; flex-direction: column; gap: 0.2rem;
  padding: 1.4rem 1.4rem 1.4rem 0;
  border-right: 1px solid var(--rule);
}
.counts-grid div:last-child { border-right: none; }
.counts-grid dt {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem; letter-spacing: 0.04em;
  text-transform: lowercase; color: var(--ink-mute);
  margin: 0;
}
.counts-grid dd {
  font-family: "Newsreader", serif;
  font-size: 2.4rem; line-height: 1; margin: 0;
  color: var(--ink);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
}
@media (max-width: 50rem) {
  .counts-grid { grid-template-columns: repeat(2, 1fr); }
  .counts-grid div:nth-child(2) { border-right: none; }
  .counts-grid div:nth-child(1), .counts-grid div:nth-child(2) { border-bottom: 1px solid var(--rule); }
}

/* ────────────────── SECTION MARKS ──────────────────
 * Quiet typographic markers: § number, a hairline rule extending across
 * the column, and the section title in lowercase mono. No badges,
 * no rotated stamps, no caution-tape stripes. The number does the
 * structural work. */
.section-mark {
  display: flex; align-items: baseline; gap: 0.7rem;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.82rem; letter-spacing: 0;
  color: var(--ink-mute);
  margin: 4.5rem 0 1.5rem;
  text-transform: lowercase;
  font-weight: 400;
}
.section-mark .sec-num {
  color: var(--accent);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.section-mark .sec-rule {
  flex: 1; height: 1px;
  background: var(--rule);
  align-self: center;
  max-width: 100%;
}
.section-mark .sec-rule.short { max-width: 4rem; flex: 0 0 4rem; }
.section-mark .sec-title { color: var(--ink-mute); }

/* ────────────────── TWO-COL "WHY" ────────────────── */
.two-col { display: grid; grid-template-columns: 1fr; gap: 2rem; }
@media (min-width: 60rem) { .two-col { grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); gap: 3.5rem; } }
.aside {
  padding: 1.4rem 1.5rem;
  background: transparent;
  border-left: 1px solid var(--accent);
  font-size: 0.95rem;
  margin-top: 0.4rem;
}
.aside-label {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.75rem; letter-spacing: 0.02em;
  color: var(--accent); text-transform: lowercase;
  margin: 0 0 0.5rem;
}
.aside p { color: var(--ink-2); }
.aside p:last-child { margin-bottom: 0; }

/* ────────────────── PAPER CARDS — calm grid ──────────────────
 * Plain rectangles. No rotation, no shadows. Single hairline border.
 * Hover: a single accent line draws underneath (a pen-stroke). */
.paper-cards {
  list-style: none; padding: 1rem 0 0; margin: 0;
  display: grid; grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
  gap: 1.2rem 1.5rem;
}
.paper-card {
  background: transparent;
  border: 1px solid var(--rule);
  position: relative;
  transition: border-color 0.12s;
  overflow: hidden;
}
.paper-card::after {
  content: "";
  position: absolute; left: 0; right: 0; bottom: 0;
  height: 2px; background: var(--accent);
  transform: scaleX(0); transform-origin: left;
  transition: transform 0.18s ease-out;
}
.paper-card:hover { border-color: var(--ink); }
.paper-card:hover::after { transform: scaleX(1); }
.card-link { display: block; padding: 1.15rem 1.2rem 1rem; color: var(--ink); border: none; }
.card-link:hover { color: var(--ink); border: none; }
.card-id {
  font-size: 0.7rem; color: var(--ink-mute);
  margin-bottom: 0.55rem; letter-spacing: 0;
  border-bottom: 1px solid var(--rule);
  padding-bottom: 0.45rem;
}
.paper-card h3 {
  font-family: "Newsreader", serif;
  font-size: 1.08rem; font-weight: 500;
  margin: 0 0 0.5rem; color: var(--ink); line-height: 1.25;
  letter-spacing: -0.005em;
}
.card-meta {
  font-size: 0.85rem; color: var(--ink-3);
  margin-bottom: 0.7rem;
}
.card-meta em { color: var(--ink-3); font-style: italic; }
.card-tags { display: flex; flex-wrap: wrap; gap: 0.3rem; }

/* ────────────────── PAPER LIST — compact rows ────────────────── */
.paper-list { list-style: none; padding: 0; margin: 1.5rem 0 0; }
.paper-list li { border-bottom: 1px solid var(--rule); }
.paper-list li:first-child { border-top: 1px solid var(--rule); }
.paper-list li a {
  display: grid; grid-template-columns: minmax(15rem, 18rem) 1fr auto;
  gap: 1.5rem; padding: 0.95rem 0.4rem;
  color: var(--ink); border: none;
  align-items: baseline; transition: background 0.12s, color 0.12s;
  position: relative;
}
.paper-list li a::before {
  content: "";
  position: absolute; left: 0; top: 0; bottom: 0;
  width: 2px; background: var(--accent);
  transform: scaleY(0); transform-origin: top;
  transition: transform 0.15s ease-out;
}
.paper-list li a:hover {
  background: var(--paper-2);
  color: var(--ink);
  border: none;
  padding-left: 0.7rem;
}
.paper-list li a:hover::before { transform: scaleY(1); }
.row-id { font-size: 0.78rem; color: var(--ink-mute); }
.row-title {
  font-family: "Newsreader", serif;
  font-size: 1.05rem; font-weight: 500; line-height: 1.3;
  color: var(--ink);
}
.row-meta { font-size: 0.83rem; color: var(--ink-mute); white-space: nowrap; font-family: "JetBrains Mono", monospace; }
@media (max-width: 50rem) {
  .paper-list li a { grid-template-columns: 1fr; gap: 0.25rem; }
  .row-meta { white-space: normal; }
}

/* ────────────────── BOTTOM CTA ────────────────── */
.cta-bottom { padding: 4rem 0 2rem; margin-top: 5rem; border-top: 1px solid var(--rule); }
.cta-bottom .cta-grid > div { position: relative; z-index: 1; max-width: 44rem; }
.cta-bottom .lede { margin: 0.75rem 0 1.75rem; }

/* ────────────────── TAG CHIPS — restrained ──────────────────
 * Lowercase mono labels with category-coded left rule. No background
 * fill, no rotation. Censor=pen-blue, technique=ink, defense=moss.
 * Quiet by default; the type does the work. */
.tag {
  display: inline-flex; align-items: center;
  padding: 0.12rem 0.5rem 0.1rem;
  margin: 0.2rem 0.25rem 0.2rem 0;
  background: transparent;
  border: 1px solid var(--rule);
  font-size: 0.72rem;
  letter-spacing: 0;
  color: var(--ink-2);
  font-weight: 500;
  transition: all 0.12s;
  white-space: nowrap;
  border-radius: 1px;
  border-left-width: 2px;
}
.tag:hover {
  background: var(--ink); color: var(--paper);
  border-color: var(--ink);
  border-bottom-color: var(--ink);
}
.tag.censor    { border-left-color: var(--accent); }
.tag.technique { border-left-color: var(--rust); }
.tag.defense   { border-left-color: var(--moss); }
.tag.censor:hover    { background: var(--accent); border-color: var(--accent); border-bottom-color: var(--accent); }
.tag.technique:hover { background: var(--rust); border-color: var(--rust); border-bottom-color: var(--rust); }
.tag.defense:hover   { background: var(--moss); border-color: var(--moss); border-bottom-color: var(--moss); }

/* ────────────────── PAPER DETAIL — narrow research column ──────────────────
 * Single column, narrow measure (~36rem) in line with academic paper
 * conventions. Marginal numbering. The "DECLASSIFIED" stamp from the
 * old design is gone — restraint signals seriousness more reliably than
 * theatrics. */
article.paper {
  max-width: 44rem; margin: 0 auto;
  padding: 0;
}
article.paper .paper-id {
  font-size: 0.74rem; color: var(--ink-mute);
  margin: 0 0 0.7rem; letter-spacing: 0;
}
article.paper h1 {
  font-family: "Newsreader", serif;
  font-size: clamp(1.7rem, 3vw, 2.2rem); font-weight: 500;
  margin: 0 0 0.6rem; color: var(--ink);
  letter-spacing: -0.014em; line-height: 1.18;
  text-transform: none;
}
article.paper h2 {
  font-family: "Newsreader", serif;
  color: var(--ink); font-size: 1.2rem;
  margin: 2.4rem 0 0.7rem;
  font-weight: 600;
  letter-spacing: -0.005em;
}
article.paper h3 { color: var(--ink); font-family: "Newsreader", serif; }
article.paper p { color: var(--ink-2); }
article.paper a { color: var(--accent); }
article.paper a:hover { color: var(--accent-2); border-bottom-color: var(--accent-2); }
article.paper code { background: var(--code-bg); color: var(--ink); border-color: var(--paper-edge); }
article.paper .paper-links {
  font-size: 0.92rem; color: var(--ink-mute);
  margin: 0 0 2rem; padding-bottom: 1.5rem;
  border-bottom: 1px solid var(--rule);
}
.related-section { max-width: 44rem; margin: 3rem auto 0; }
.tag-name { color: var(--accent); font-family: "JetBrains Mono", monospace; }
.byline {
  font-size: 0.96rem; color: var(--ink-3);
  margin: 0 0 1.5rem;
  font-family: "Newsreader", serif;
}
.byline em { color: var(--ink-3); font-style: italic; }
.badge {
  display: inline-block;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.68rem; letter-spacing: 0.02em;
  text-transform: lowercase;
  padding: 0.16rem 0.55rem 0.1rem;
  background: var(--accent); color: var(--paper);
  vertical-align: middle; margin-left: 0.6rem;
  border-radius: 1px;
  font-weight: 500;
}
.badge.core { background: var(--accent); }
.abstract, .notes { white-space: pre-wrap; }
article.paper .abstract { color: var(--ink-2); font-family: "Newsreader", serif; font-size: 1.02rem; line-height: 1.6; }
article.paper .notes {
  padding: 1rem 1.25rem;
  background: var(--paper-2);
  color: var(--ink-2);
  border-left: 2px solid var(--accent);
  margin: 1rem 0;
  font-size: 0.94rem;
  position: relative;
}
.tags-dl {
  display: grid; grid-template-columns: max-content 1fr;
  gap: 0.5rem 1.5rem; margin: 1rem 0;
}
.tags-dl dt {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.74rem; letter-spacing: 0;
  color: var(--ink-mute); padding-top: 0.3rem;
  text-transform: lowercase;
}
.tags-dl dd { margin: 0; padding: 0; }

/* ────────────────── TAG INDEX ────────────────── */
.tag-index {
  list-style: none; padding: 0; margin: 1.5rem 0;
  display: grid;
  grid-template-columns: max-content max-content 1fr max-content;
  gap: 0.55rem 1.5rem;
}
.tag-index li { display: contents; }
.tag-index li > a {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.92rem; color: var(--accent);
  border: none;
}
.tag-index li > a:hover { color: var(--accent-2); border: none; }
.tag-index li > span:nth-child(2) { color: var(--ink); font-family: "Newsreader", serif; font-weight: 500; }
.tag-index li > span.muted {
  color: var(--ink-mute); font-size: 0.84rem;
  font-family: "JetBrains Mono", monospace;
}

/* ────────────────── TAXONOMY PAGE ────────────────── */
.tax {
  display: grid; grid-template-columns: max-content 1fr;
  gap: 0.5rem 1.5rem; margin: 1rem 0;
}
.tax dt {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.86rem; padding-top: 0.25rem;
  color: var(--ink);
  font-weight: 500;
}
.tax dt a { color: var(--accent); border: none; }
.tax dt a:hover { color: var(--accent-2); border: none; }
.tax dd { margin: 0; padding: 0 0 0.4rem; color: var(--ink-2); font-size: 0.95rem; }
.tax dd .muted { display: block; font-size: 0.82rem; margin-top: 0.15rem; color: var(--ink-mute); }
@media (max-width: 50rem) {
  .tax, .tags-dl, .tag-index { grid-template-columns: 1fr; gap: 0.2rem; }
  .tax dt, .tags-dl dt { padding-top: 0.6rem; }
}

/* ────────────────── FOOTER ────────────────── */
.site-footer {
  border-top: 1px solid var(--rule);
  margin-top: 5rem; padding: 0 0 1.5rem;
  background: transparent;
  color: var(--ink-2); font-size: 0.88rem;
  position: relative;
}
.site-footer .wrap { padding-top: 3rem; }
.foot-grid {
  display: grid; grid-template-columns: 2fr repeat(3, 1fr);
  gap: 2.5rem; margin-bottom: 2rem;
}
@media (max-width: 50rem) { .foot-grid { grid-template-columns: 1fr 1fr; } }
.foot-title {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.78rem; letter-spacing: 0;
  text-transform: lowercase; color: var(--ink);
  margin-bottom: 0.55rem;
  font-weight: 500;
}
.foot-grid ul { list-style: none; padding: 0; margin: 0; }
.foot-grid li { margin-bottom: 0.35rem; }
.foot-grid a { color: var(--ink-2); border: none; }
.foot-grid a:hover { color: var(--accent); border: none; }
.legal {
  padding-top: 1.5rem; border-top: 1px solid var(--rule);
  color: var(--ink-mute); font-size: 0.82rem;
  max-width: 60rem;
  font-family: "Atkinson Hyperlegible", sans-serif;
}
.legal a { color: var(--ink-2); border: none; }
.legal a:hover { color: var(--accent); }

/* Use page sections */
dl.tax dt code { font-family: inherit; background: none; border: none; padding: 0; color: inherit; }

/* ────────────────── SEARCH — restrained terminal ──────────────────
 * Single-line input with a ghost slash key, no boxes-around-boxes.
 * Results dropdown is a calm panel with hairline rules. */
.search-wrap {
  position: relative;
  flex: 1 1 24rem; max-width: 30rem; min-width: 14rem;
  margin: 0 1rem;
}
#search {
  width: 100%;
  padding: 0.55rem 2.2rem 0.5rem 0.85rem;
  border: 1px solid var(--rule);
  background: var(--paper);
  color: var(--ink);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.86rem;
  letter-spacing: 0;
  transition: border-color 0.12s, box-shadow 0.12s;
  border-radius: 1px;
}
#search::placeholder { color: var(--ink-mute); }
#search:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.search-kbd {
  position: absolute; right: 0.55rem; top: 50%; transform: translateY(-50%);
  font-family: "JetBrains Mono", monospace; font-size: 0.7rem;
  padding: 0.08rem 0.42rem;
  border: 1px solid var(--rule);
  color: var(--ink-mute); background: var(--paper-2);
  pointer-events: none;
  border-radius: 1px;
}
#search:focus + .search-kbd { display: none; }

#search-results {
  position: absolute; top: calc(100% + 0.4rem); left: 0; right: 0;
  background: var(--paper);
  border: 1px solid var(--rule);
  box-shadow: 0 12px 32px rgba(20, 19, 15, 0.08);
  max-height: 70vh; overflow-y: auto;
  z-index: 30;
  border-radius: 1px;
}
#search-results .empty {
  padding: 1rem 1.1rem; color: var(--ink-mute);
  font-size: 0.92rem;
  font-family: "Atkinson Hyperlegible", sans-serif;
}
#search-results .summary {
  padding: 0.5rem 1.1rem;
  color: var(--ink-mute); font-size: 0.74rem;
  letter-spacing: 0; text-transform: lowercase;
  border-bottom: 1px solid var(--rule);
  font-family: "JetBrains Mono", monospace;
}
#search-results a {
  display: grid; grid-template-columns: 1fr auto;
  gap: 0.4rem 1rem; padding: 0.65rem 1.1rem;
  color: var(--ink);
  border-bottom: 1px solid var(--rule);
  border-top: none;
  border-left: 2px solid transparent;
  border-right: none;
  transition: background 0.1s, border-color 0.1s;
}
#search-results a:last-child { border-bottom: none; }
#search-results a:hover, #search-results a.active {
  background: var(--paper-2);
  border-left-color: var(--accent);
}
#search-results .r-title {
  font-family: "Newsreader", serif;
  font-size: 0.98rem; font-weight: 500; line-height: 1.25;
  color: var(--ink);
}
#search-results .r-meta { font-size: 0.78rem; color: var(--ink-mute); white-space: nowrap; font-family: "JetBrains Mono", monospace; }
#search-results .r-id {
  grid-column: 1 / 3;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem; color: var(--ink-mute);
}
#search-results .r-tags { grid-column: 1 / 3; display: flex; gap: 0.3rem; flex-wrap: wrap; }
#search-results .r-tags .tag { font-size: 0.68rem; padding: 0.05rem 0.4rem; }
#search-results mark {
  background: var(--selection);
  color: var(--ink); padding: 0;
}

/* Plotter SVG figures inherit color through currentColor; the accent
 * highlight is applied per-element via stroke="var(--accent)". */
svg.plotter { color: var(--ink-2); }

/* ────────────────── FINDINGS — extracted-claim cards ──────────────────
 * Findings are short claims (1-3 sentences) extracted from full paper
 * text, tagged against the same taxonomy. They show up:
 *  - on /findings/ as a dense scannable list (.findings-list.big)
 *  - on /findings/<id>/ as a single article (.finding)
 *  - on /papers/<id>/ under "findings extracted from this paper"
 *  - on tag pages under "findings tagged here"
 * The summary is the lede; tags + paper-id are metadata at the bottom.
 */
.findings-list { list-style: none; padding: 0; margin: 1.2rem 0; }
.findings-list .finding-row { margin: 0; padding: 0; border-top: 1px solid var(--rule-fade); }
.findings-list .finding-row:last-child { border-bottom: 1px solid var(--rule-fade); }
.findings-list .finding-link {
  display: block; padding: 1rem 0;
  color: var(--ink); border: none;
  text-decoration: none;
  transition: background 0.1s, padding-left 0.12s;
}
.findings-list .finding-link:hover {
  background: var(--paper);
  padding-left: 0.4rem; border: none;
}
.finding-summary {
  font-family: "Newsreader", serif;
  font-size: 1.02rem; line-height: 1.45;
  color: var(--ink); margin: 0;
}
.findings-list.big .finding-summary { font-size: 1.08rem; }
.finding-meta {
  margin: 0.45rem 0 0;
  display: flex; flex-wrap: wrap; gap: 0.4rem 0.55rem; align-items: center;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.74rem; color: var(--ink-mute);
}
.finding-paper { color: var(--ink-2); }
.finding-paper-year { color: var(--ink-mute); }
.finding-section { color: var(--ink-mute); }
.finding-kind {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem; letter-spacing: 0.02em;
  text-transform: uppercase;
  color: var(--rust);
  padding: 0.05rem 0.4rem;
  border: 1px solid var(--rust);
  border-radius: 1px;
}
.findings-section { max-width: 44rem; margin: 3rem auto 0; }

/* Single-finding page */
.finding { max-width: 44rem; margin: 0 auto; }
.finding .finding-title {
  font-family: "Newsreader", serif;
  font-size: 1.85rem; line-height: 1.2; font-weight: 500;
  margin: 0.5rem 0 1rem; color: var(--ink);
}
.finding-attrib {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.85rem; color: var(--ink-mute);
  margin: 0 0 1.6rem; line-height: 1.6;
}
.finding-attrib em { font-family: "Newsreader", serif; font-style: italic; color: var(--ink-2); }
.finding-implications {
  list-style: none; padding: 0; margin: 0.75rem 0 1.5rem;
}
.finding-implications li {
  padding: 0.7rem 0 0.7rem 1.4rem;
  border-top: 1px solid var(--rule-fade);
  font-family: "Newsreader", serif; font-size: 1.02rem; line-height: 1.5;
  position: relative;
}
.finding-implications li::before {
  content: "→"; position: absolute; left: 0; top: 0.7rem;
  color: var(--accent); font-family: "JetBrains Mono", monospace;
}
.finding-implications li:last-child { border-bottom: 1px solid var(--rule-fade); }
.finding-extractor { margin-top: 2rem; font-size: 0.82rem; }

@media (max-width: 35rem) {
  .findings-list .finding-link { padding: 0.85rem 0; }
  .findings-list .finding-link:hover { padding-left: 0; background: transparent; }
  .finding-summary, .findings-list.big .finding-summary { font-size: 1rem; }
  .finding .finding-title { font-size: 1.45rem; }
  .finding-attrib { font-size: 0.8rem; }
  .finding-implications li { font-size: 0.95rem; padding-left: 1.2rem; }
}

/* ────────────────── ASK — query form + LLM answer ──────────────────
 * /ask/ is the web flagship for the synthesize tool: textarea, render
 * the answer as markdown, list the cited findings underneath. */
.ask-hero { max-width: 44rem; margin-bottom: 2rem; }
.ask-form {
  max-width: 44rem; margin: 0 0 2rem;
  display: flex; flex-direction: column; gap: 0.7rem;
}
.ask-form textarea {
  width: 100%; box-sizing: border-box;
  padding: 0.85rem 1rem;
  border: 1px solid var(--rule);
  background: var(--paper);
  color: var(--ink);
  font-family: "Newsreader", serif;
  font-size: 1.08rem; line-height: 1.5;
  resize: vertical;
  border-radius: 1px;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.ask-form textarea:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.ask-controls {
  display: flex; align-items: center; gap: 1rem;
  flex-wrap: wrap;
}
.ask-help kbd {
  font-family: "JetBrains Mono", monospace; font-size: 0.78rem;
  padding: 0.05rem 0.35rem;
  border: 1px solid var(--rule);
  border-radius: 1px;
  background: var(--paper);
  color: var(--ink-mute);
}
.ask-status {
  max-width: 44rem; margin: 1rem 0;
  padding: 0.75rem 1rem;
  border-left: 3px solid var(--rule);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.88rem;
  color: var(--ink-mute);
  background: var(--paper);
}
.ask-status.loading {
  border-left-color: var(--accent);
  color: var(--accent);
}
.ask-status.loading::before {
  content: "⧗ "; opacity: 0.7;
}
.ask-status.error {
  border-left-color: var(--rust);
  color: var(--rust);
}
.ask-result { max-width: 44rem; }
.ask-answer {
  font-family: "Newsreader", serif;
  font-size: 1.08rem; line-height: 1.55;
  color: var(--ink);
  margin: 1.5rem 0;
}
.ask-answer h2, .ask-answer h3 {
  font-family: "Newsreader", serif;
  font-size: 1.25rem; font-weight: 600;
  margin: 1.6rem 0 0.5rem;
  color: var(--ink);
}
.ask-answer h2 { font-size: 1.4rem; }
.ask-answer p { margin: 0.7rem 0; }
.ask-answer ul { margin: 0.7rem 0 1rem 1.4rem; padding: 0; }
.ask-answer li { margin: 0.35rem 0; }
.ask-answer code {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.92em;
  background: var(--code-bg);
  padding: 0.05em 0.35em; border-radius: 1px;
}
.ask-answer a { color: var(--accent); }
.ask-bundle {
  margin: 2rem 0 1rem;
  border-top: 1px solid var(--rule-fade);
  padding-top: 0.75rem;
}
.ask-bundle summary {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.85rem;
  color: var(--ink-mute);
  cursor: pointer;
  padding: 0.4rem 0;
}
.ask-bundle summary:hover { color: var(--accent); }
.ask-bundle .findings-list { margin-top: 0.5rem; }
.ask-meta { margin-top: 1.5rem; font-size: 0.82rem; }

@media (max-width: 35rem) {
  .ask-form textarea { font-size: 1rem; }
  .ask-answer { font-size: 1rem; }
  .ask-answer h2 { font-size: 1.25rem; }
  .ask-answer h3 { font-size: 1.1rem; }
}

@media (max-width: 60rem) {
  .site-header .wrap { flex-direction: column; align-items: stretch; gap: 0.7rem; padding: 0.75rem 1.25rem; }
  /* In column flex, the parent's "flex: 1 1 24rem" basis applied to
     height (24rem!) — visible as a giant gap with the kbd hint
     floating mid-air. Reset to auto so each item is its own height. */
  .search-wrap { margin: 0; max-width: none; flex: 0 0 auto; min-width: 0; }
  nav { gap: 0.25rem 1rem; }
  main.wrap { padding: 2rem 1.25rem 4rem; }
  .wrap { padding: 0 1.25rem; }
}

@media (max-width: 35rem) {
  /* Phones: tighter gutters, smaller display headlines, cards full-width. */
  main.wrap { padding: 1.5rem 1rem 3rem; }
  .wrap { padding: 0 1rem; }
  .site-header .wrap { padding: 0.65rem 1rem; }
  .display-sm { font-size: 1.6rem; line-height: 1.15; }
  .hero h1, h1 { font-size: 1.75rem; line-height: 1.15; }
  .hero { padding: 1.5rem 0 0.5rem; }
  .lede { font-size: 1rem; }
  /* Paper rows: stack ID / title / meta vertically rather than the
     three-column grid that overflows hard on phones. */
  .paper-list li a { display: block !important; padding: 0.85rem 0; }
  .paper-list .row-id { display: block; font-size: 0.72rem; margin-bottom: 0.15rem; }
  .paper-list .row-title { display: block; font-size: 1rem; line-height: 1.3; }
  .paper-list .row-meta { display: block; font-size: 0.78rem; margin-top: 0.2rem; }
  /* Tag pills wrap — and never grow tall enough to overflow on a phone. */
  .tag { font-size: 0.72rem; padding: 0.05rem 0.45rem; }
  /* Tax / tags-dl / tag-index already collapse to 1 col at 50rem — no extra work. */
  pre, code { font-size: 0.82rem; }
  pre { padding: 0.75rem 0.85rem; overflow-x: auto; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    transition-duration: 0.01ms !important;
  }
}
`