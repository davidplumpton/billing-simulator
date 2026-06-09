package app

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

const (
	invoicePDFPageWidth    = 612.0
	invoicePDFPageHeight   = 792.0
	invoicePDFMarginLeft   = 48.0
	invoicePDFMarginRight  = 48.0
	invoicePDFMarginTop    = 54.0
	invoicePDFMarginBottom = 54.0
)

type invoicePDFTextRun struct {
	Font string
	Size float64
	X    float64
	Y    float64
	Text string
}

type invoicePDFTableColumn struct {
	Header string
	Width  float64
}

type invoicePDFRenderer struct {
	pages [][]invoicePDFTextRun
	y     float64
}

// invoicePDFBytes renders the printable invoice view model into a self-contained PDF document.
func invoicePDFBytes(data invoicePageData) ([]byte, error) {
	if !data.Loaded {
		return nil, fmt.Errorf("invoice %q is not loaded", data.InvoiceID)
	}
	renderer := newInvoicePDFRenderer()
	renderer.invoiceHeader(data)
	renderer.invoiceKeyValues("Invoice Summary", [][2]string{
		{"Bill", data.BillID},
		{"Billing period", data.BillingPeriod},
		{"Invoice date", data.InvoiceDate},
		{"Due date", data.DueDate},
		{"Payer account", data.PayerAccountID},
		{"Document status", data.DocumentStatus},
		{"Payment status", data.PaymentStatus},
		{"Amount due", data.AmountDue},
		{"Amount paid", data.AmountPaid},
	})
	renderer.invoiceKeyValues("Seller and Bill To", [][2]string{
		{"Seller", data.SellerOfRecord},
		{"Seller address", data.SellerAddress},
		{"Seller tax registration", data.SellerTaxRegistration},
		{"Bill to", data.BillToName},
		{"Bill-to email", data.BillToEmail},
		{"Bill-to address", data.BillToAddress},
		{"Bill-to tax registration", data.BillToTaxRegistration},
	})
	renderer.invoiceTable("Charge Summary", []invoicePDFTableColumn{
		{Header: "Metric", Width: 130},
		{Header: "Value", Width: 90},
		{Header: "Metric", Width: 130},
		{Header: "Value", Width: 90},
	}, [][]string{
		{"Charges", data.Charges, "Credits", data.Credits},
		{"Refunds", data.Refunds, "Tax", data.Tax},
		{"Line items", strconv.Itoa(data.LineItemCount), "Total", data.Total},
	})

	serviceRows := make([][]string, 0, len(data.ServiceSummaries))
	for _, summary := range data.ServiceSummaries {
		serviceRows = append(serviceRows, []string{
			strings.TrimSpace(summary.ServiceName + " " + summary.ServiceCode + " " + summary.CurrencyCode),
			strconv.Itoa(summary.LineItemCount),
			summary.Charges,
			summary.Credits,
			summary.Refunds,
			summary.Tax,
			summary.Total,
		})
	}
	renderer.invoiceTable("Service Detail", []invoicePDFTableColumn{
		{Header: "Service", Width: 195},
		{Header: "Items", Width: 42},
		{Header: "Charges", Width: 56},
		{Header: "Credits", Width: 56},
		{Header: "Refunds", Width: 56},
		{Header: "Tax", Width: 50},
		{Header: "Total", Width: 61},
	}, serviceRows)

	accountRows := make([][]string, 0, len(data.AccountSummaries))
	for _, summary := range data.AccountSummaries {
		accountRows = append(accountRows, []string{
			strings.TrimSpace(summary.UsageAccountID + " " + summary.CurrencyCode),
			strconv.Itoa(summary.LineItemCount),
			summary.Charges,
			summary.Credits,
			summary.Refunds,
			summary.Tax,
			summary.Total,
		})
	}
	renderer.invoiceTable("Account Detail", []invoicePDFTableColumn{
		{Header: "Usage account", Width: 195},
		{Header: "Items", Width: 42},
		{Header: "Charges", Width: 56},
		{Header: "Credits", Width: 56},
		{Header: "Refunds", Width: 56},
		{Header: "Tax", Width: 50},
		{Header: "Total", Width: 61},
	}, accountRows)

	lineRows := make([][]string, 0, len(data.LineItems))
	for _, item := range data.LineItems {
		resource := item.Resource
		if strings.TrimSpace(item.ResourceID) != "" && item.ResourceID != item.Resource {
			resource = strings.TrimSpace(resource + " " + item.ResourceID)
		}
		detail := strings.TrimSpace(item.UsageType + " " + item.Operation + " " + item.Window + " " + item.Description)
		if detail != "" {
			resource = strings.TrimSpace(resource + " " + detail)
		}
		lineRows = append(lineRows, []string{
			resource,
			item.UsageAccountID,
			strings.TrimSpace(item.ServiceName + " " + item.ServiceCode),
			item.LineItemType,
			item.RegionCode,
			item.Quantity,
			item.Rate,
			item.Cost,
		})
	}
	renderer.invoiceTable("Invoice Lines", []invoicePDFTableColumn{
		{Header: "Resource and usage", Width: 100},
		{Header: "Account", Width: 65},
		{Header: "Service", Width: 90},
		{Header: "Type", Width: 44},
		{Header: "Region", Width: 45},
		{Header: "Quantity", Width: 58},
		{Header: "Rate", Width: 65},
		{Header: "Cost", Width: 49},
	}, lineRows)

	return buildInvoicePDF(renderer.pages)
}

// newInvoicePDFRenderer starts a fixed letter-size PDF layout with simple text and tables.
func newInvoicePDFRenderer() *invoicePDFRenderer {
	renderer := &invoicePDFRenderer{}
	renderer.addPage()
	return renderer
}

func (r *invoicePDFRenderer) addPage() {
	r.pages = append(r.pages, nil)
	r.y = invoicePDFPageHeight - invoicePDFMarginTop
}

func (r *invoicePDFRenderer) invoiceHeader(data invoicePageData) {
	r.ensureSpace(44)
	y := r.y
	r.draw("F2", 18, invoicePDFMarginLeft, y, "Invoice "+data.InvoiceID)
	r.draw("F2", 16, 420, y, "Total "+data.Total)
	r.draw("F1", 9, invoicePDFMarginLeft, y-17, "AWS Billing Simulator synthetic invoice")
	r.draw("F1", 9, 420, y-17, data.PaymentStatus+" due "+data.AmountDue)
	r.y -= 42
}

func (r *invoicePDFRenderer) invoiceKeyValues(title string, rows [][2]string) {
	r.sectionTitle(title)
	for _, row := range rows {
		for idx, line := range invoicePDFWrapText(row[1], 360, 8) {
			r.ensureSpace(11)
			if idx == 0 {
				r.draw("F2", 8, invoicePDFMarginLeft, r.y, row[0]+":")
			}
			r.draw("F1", 8, invoicePDFMarginLeft+118, r.y, line)
			r.y -= 10
		}
		r.y -= 1
	}
	r.y -= 6
}

func (r *invoicePDFRenderer) invoiceTable(title string, columns []invoicePDFTableColumn, rows [][]string) {
	r.sectionTitle(title)
	if len(rows) == 0 {
		r.line("No rows")
		r.y -= 4
		return
	}
	headerHeight := 13.0
	rowLeading := 9.0
	fontSize := 7.2
	drawHeader := func() {
		x := invoicePDFMarginLeft
		y := r.y
		for _, column := range columns {
			r.draw("F2", 7.5, x, y, column.Header)
			x += column.Width
		}
		r.y -= headerHeight
	}
	r.ensureSpace(headerHeight + rowLeading)
	drawHeader()
	for _, row := range rows {
		wrapped := make([][]string, len(columns))
		lineCount := 1
		for idx, column := range columns {
			value := ""
			if idx < len(row) {
				value = row[idx]
			}
			wrapped[idx] = invoicePDFWrapText(value, column.Width-4, fontSize)
			if len(wrapped[idx]) > lineCount {
				lineCount = len(wrapped[idx])
			}
		}
		rowHeight := float64(lineCount)*rowLeading + 3
		if r.y-rowHeight < invoicePDFMarginBottom {
			r.addPage()
			drawHeader()
		}
		x := invoicePDFMarginLeft
		rowY := r.y
		for idx, column := range columns {
			for lineIdx, line := range wrapped[idx] {
				r.draw("F1", fontSize, x, rowY-float64(lineIdx)*rowLeading, line)
			}
			x += column.Width
		}
		r.y -= rowHeight
	}
	r.y -= 8
}

func (r *invoicePDFRenderer) sectionTitle(title string) {
	r.ensureSpace(24)
	r.draw("F2", 11, invoicePDFMarginLeft, r.y, title)
	r.y -= 15
}

func (r *invoicePDFRenderer) line(text string) {
	r.ensureSpace(12)
	r.draw("F1", 8, invoicePDFMarginLeft, r.y, text)
	r.y -= 11
}

func (r *invoicePDFRenderer) ensureSpace(height float64) {
	if r.y-height < invoicePDFMarginBottom {
		r.addPage()
	}
}

func (r *invoicePDFRenderer) draw(font string, size, x, y float64, text string) {
	pageIndex := len(r.pages) - 1
	r.pages[pageIndex] = append(r.pages[pageIndex], invoicePDFTextRun{
		Font: font,
		Size: size,
		X:    x,
		Y:    y,
		Text: text,
	})
}

func buildInvoicePDF(pages [][]invoicePDFTextRun) ([]byte, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("invoice PDF requires at least one page")
	}
	const (
		catalogObject = 1
		pagesObject   = 2
		regularFont   = 3
		boldFont      = 4
		firstPageObj  = 5
	)

	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := []int{0}
	writeObject := func(objectID int, text string) {
		for len(offsets) <= objectID {
			offsets = append(offsets, 0)
		}
		offsets[objectID] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", objectID, text)
	}

	kids := make([]string, 0, len(pages))
	for idx := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPageObj+idx*2))
	}
	writeObject(catalogObject, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(pagesObject, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	writeObject(regularFont, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	writeObject(boldFont, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>")

	for idx, page := range pages {
		pageObjectID := firstPageObj + idx*2
		contentObjectID := pageObjectID + 1
		writeObject(pageObjectID, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>", invoicePDFPageWidth, invoicePDFPageHeight, regularFont, boldFont, contentObjectID))
		stream := invoicePDFContentStream(page, idx+1, len(pages))
		writeObject(contentObjectID, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}

	xrefOffset := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n", len(offsets))
	body.WriteString("0000000000 65535 f \n")
	for objectID := 1; objectID < len(offsets); objectID++ {
		fmt.Fprintf(&body, "%010d 00000 n \n", offsets[objectID])
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xrefOffset)
	return body.Bytes(), nil
}

func invoicePDFContentStream(page []invoicePDFTextRun, pageNumber, pageCount int) string {
	var stream strings.Builder
	for _, run := range page {
		fmt.Fprintf(&stream, "BT /%s %.2f Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n", run.Font, run.Size, run.X, run.Y, invoicePDFEscapeText(run.Text))
	}
	footer := fmt.Sprintf("AWS Billing Simulator - synthetic training invoice - Page %d of %d", pageNumber, pageCount)
	fmt.Fprintf(&stream, "BT /F1 7.00 Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n", invoicePDFMarginLeft, 30.0, invoicePDFEscapeText(footer))
	return stream.String()
}

func invoicePDFWrapText(text string, maxWidth, fontSize float64) []string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return []string{""}
	}
	maxChars := int(maxWidth / (fontSize * 0.52))
	if maxChars < 4 {
		maxChars = 4
	}
	words := strings.Fields(text)
	lines := make([]string, 0, len(words))
	current := ""
	for _, word := range words {
		for invoicePDFRuneLen(word) > maxChars {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			piece, rest := invoicePDFSplitRunes(word, maxChars)
			lines = append(lines, piece)
			word = rest
		}
		if current == "" {
			current = word
			continue
		}
		if invoicePDFRuneLen(current)+1+invoicePDFRuneLen(word) <= maxChars {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func invoicePDFRuneLen(text string) int {
	return len([]rune(text))
}

func invoicePDFSplitRunes(text string, limit int) (string, string) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, ""
	}
	return string(runes[:limit]), string(runes[limit:])
}

func invoicePDFEscapeText(text string) string {
	var builder strings.Builder
	for _, r := range text {
		switch r {
		case '\\', '(', ')':
			builder.WriteByte('\\')
			builder.WriteRune(r)
		case '\n', '\r', '\t':
			builder.WriteByte(' ')
		default:
			if r < 32 {
				continue
			}
			if r > 126 {
				builder.WriteByte('?')
				continue
			}
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
