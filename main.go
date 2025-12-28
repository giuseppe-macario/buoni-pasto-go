package main

import (
	"bytes"
	"fmt"
	// "log"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/ledongthuc/pdf"
)

// ======================================================================================
// 1. MODEL (Domain Layer)
// Rappresenta i dati puri, senza conoscenza della GUI.
// ======================================================================================

type TimesheetEntry struct {
	Date       string
	DayOfWeek  string
	EntryTime  string
	ExitTime   string
	MealStatus string // "PRANZO", "CENA", "PRANZO e CENA"
	Note       string
}

// ======================================================================================
// 2. BUSINESS LOGIC LAYER (Service)
// Contiene la logica di parsing e le regole di business.
// Implementa il pattern "Adapter" convertendo il PDF grezzo in oggetti Model.
// ======================================================================================

type PDFService struct{}

var (
	dateRe   = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	orarioRe = regexp.MustCompile(`(\d{1,2}[:.,]\d{2})`)
	days     = []string{"domenica", "lunedì", "martedì", "mercoledì", "giovedì", "venerdì", "sabato"}
)

func (s *PDFService) ProcessFile(path string) ([]TimesheetEntry, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []TimesheetEntry

	// Iteriamo su tutte le pagine
	for pageIndex := 1; pageIndex <= r.NumPage(); pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		rows, _ := p.GetTextByRow()
		for _, row := range rows {
			// Ricostruzione riga
			var lineBuffer bytes.Buffer
			for _, word := range row.Content {
				lineBuffer.WriteString(word.S)
				lineBuffer.WriteString(" ")
			}
			line := lineBuffer.String()

			// Parsing riga
			parsed, err := s.parseLine(line)
			if err != nil {
				continue // Riga non valida o non contiene dati rilevanti
			}

			// Calcolo Pasto
			mealType := s.calculateMeal(parsed.dateStr, parsed.exitTime)
			if mealType == "" {
				continue // Nessun pasto maturato
			}

			// Formattazione
			wdIt := s.getWeekday(parsed.dateStr)
			entryStr := parsed.entryTime.Format("15:04")
			
			// Logica asterisco per recupero compensativo
			if parsed.causale == "RECUPERO COMPENSATIVO" && parsed.entryTime.Hour() == 7 && parsed.entryTime.Minute() == 30 {
				entryStr = "*07:30"
			}

			entries = append(entries, TimesheetEntry{
				Date:       parsed.dateStr,
				DayOfWeek:  wdIt,
				EntryTime:  entryStr,
				ExitTime:   parsed.exitTime.Format("15:04"),
				MealStatus: mealType,
				Note:       parsed.causale,
			})
		}
	}

	return entries, nil
}

// Struttura interna di supporto per il parsing
type rawParsedData struct {
	dateStr   string
	entryTime time.Time
	exitTime  time.Time
	causale   string
}

func (s *PDFService) parseLine(line string) (*rawParsedData, error) {
	line = strings.TrimLeft(line, "* ")
	line = strings.TrimSpace(line)

	dateMatch := dateRe.FindStringSubmatch(line)
	if dateMatch == nil {
		return nil, fmt.Errorf("no date")
	}

	timeMatches := orarioRe.FindAllString(line, -1)
	timeIndices := orarioRe.FindAllStringIndex(line, -1)
	if len(timeMatches) < 2 {
		return nil, fmt.Errorf("no times")
	}

	oraIng := s.normalizeTime(timeMatches[0])
	oraUsc := s.normalizeTime(timeMatches[1])

	// Ignora 00:00 - 00:00
	if oraIng.IsZero() && oraUsc.IsZero() {
		return nil, fmt.Errorf("zero times")
	}

	// Estrazione causale (euristica posizionale)
	var startIdx, endIdx int
	if len(timeIndices) >= 3 {
		startIdx = timeIndices[2][1]
	} else {
		startIdx = timeIndices[1][1]
	}
	if len(timeIndices) >= 4 {
		endIdx = timeIndices[3][0]
	} else {
		endIdx = len(line)
	}

	if startIdx > len(line) { startIdx = len(line) }
	if endIdx > len(line) { endIdx = len(line) }
	
	causaleRaw := line[startIdx:endIdx]
	causale := strings.Join(strings.Fields(causaleRaw), " ") // Rimuove spazi extra
	causale = strings.Trim(causale, "-.,; ")
	
	// Pulizia specifica richiesta
	if causale == "COMANDO E LOGISTICA" {
		causale = ""
	}

	return &rawParsedData{
		dateStr:   dateMatch[1],
		entryTime: oraIng,
		exitTime:  oraUsc,
		causale:   causale,
	}, nil
}

func (s *PDFService) normalizeTime(tstr string) time.Time {
	tstr = strings.ReplaceAll(tstr, ",", ":")
	tstr = strings.ReplaceAll(tstr, ".", ":")
	t, _ := time.Parse("15:04", tstr)
	return t
}

func (s *PDFService) getWeekday(dateStr string) string {
	d, _ := time.Parse("02/01/2006", dateStr)
	return days[d.Weekday()]
}

func (s *PDFService) calculateMeal(dateStr string, exitTime time.Time) string {
	d, _ := time.Parse("02/01/2006", dateStr)
	wd := d.Weekday()
	
	minExit := exitTime.Hour()*60 + exitTime.Minute()
	
	// Regole:
	// Pranzo: Ven, Sab, Dom se uscita >= 15:30 (930 min)
	isLunchDay := wd == time.Friday || wd == time.Saturday || wd == time.Sunday
	hasLunch := isLunchDay && minExit >= 930
	
	// Cena: Sempre se uscita >= 20:30 (1230 min)
	hasDinner := minExit >= 1230

	if hasLunch && hasDinner { return "PRANZO e CENA" }
	if hasDinner { return "CENA" }
	if hasLunch { return "PRANZO" }
	return ""
}

// ======================================================================================
// 3. CONTROLLER & VIEW
// Gestisce l'UI e orchestra le chiamate al Model/Service.
// ======================================================================================

type AppController struct {
	window  fyne.Window
	service *PDFService
	data    []TimesheetEntry
	table   *widget.Table
	status  *widget.Label
}

func NewAppController(w fyne.Window) *AppController {
	return &AppController{
		window:  w,
		service: &PDFService{},
		data:    []TimesheetEntry{},
	}
}

// BuildUI costruisce l'interfaccia grafica
func (c *AppController) BuildUI() {
	// 1. Header
	title := widget.NewLabelWithStyle("Calcolatore Buoni Pasto", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	
	// 2. Status Label
	c.status = widget.NewLabel("Seleziona un file PDF per iniziare...")
	c.status.Alignment = fyne.TextAlignCenter

	// 3. Table Configuration
	c.table = widget.NewTable(
		// Length: quante righe e colonne
		func() (int, int) {
			return len(c.data), 5 // 5 colonne
		},
		// Create: crea l'elemento grafico della cella
		func() fyne.CanvasObject {
			return widget.NewLabel("Cell Content")
		},
		// Update: aggiorna i dati della cella
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			entry := c.data[id.Row]
			switch id.Col {
			case 0:
				label.SetText(fmt.Sprintf("%s (%s)", entry.Date, entry.DayOfWeek))
			case 1:
				label.SetText(entry.EntryTime)
			case 2:
				label.SetText(entry.ExitTime)
			case 3:
				label.SetText(entry.MealStatus)
				label.TextStyle = fyne.TextStyle{Bold: true}
			case 4:
				label.SetText(entry.Note)
			}
		},
	)
	
	// Imposta larghezza colonne
	c.table.SetColumnWidth(0, 180)
	c.table.SetColumnWidth(1, 80)
	c.table.SetColumnWidth(2, 80)
	c.table.SetColumnWidth(3, 150)
	c.table.SetColumnWidth(4, 250)

	// 4. Toolbar / Buttons
	btnOpen := widget.NewButton("Apri PDF", c.handleOpenFile)
	btnOpen.Importance = widget.HighImportance

	// Layout
	content := container.NewBorder(
		container.NewVBox(title, btnOpen, c.status, widget.NewSeparator()), // Top
		nil, // Bottom
		nil, // Left
		nil, // Right
		c.table, // Center (si espande)
	)

	c.window.SetContent(content)
}

// handleOpenFile gestisce l'evento click (Command Pattern)
func (c *AppController) handleOpenFile() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, c.window)
			return
		}
		if reader == nil {
			return // Utente ha annullato
		}
		defer reader.Close()

		filePath := reader.URI().Path()
		c.status.SetText("Elaborazione in corso: " + reader.URI().Name())
		
		// Esegui parsing
		entries, err := c.service.ProcessFile(filePath)
		if err != nil {
			dialog.ShowError(fmt.Errorf("Errore lettura PDF: %v", err), c.window)
			c.status.SetText("Errore durante l'elaborazione.")
			return
		}

		// Aggiorna Dati e UI
		c.data = entries
		c.table.Refresh() // Notifica la tabella che i dati sono cambiati
		
		if len(entries) == 0 {
			c.status.SetText("Nessun buono pasto trovato nel file.")
		} else {
			c.status.SetText(fmt.Sprintf("Trovati %d giorni con diritto al pasto.", len(entries)))
		}

	}, c.window)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".pdf"}))
	fd.Show()
}

// ======================================================================================
// MAIN
// ======================================================================================

func main() {
	// Inizializzazione App Fyne
	a := app.New()
	w := a.NewWindow("Gestione Buoni Pasto - Militari")

	w.Resize(fyne.NewSize(800, 600))

	// Inizializza Controller e costruisci UI
	ctrl := NewAppController(w)
	ctrl.BuildUI()

	w.ShowAndRun()
}