import re
import sys
import os
from datetime import datetime, time
import pdfplumber

# ------------------ Argomenti ------------------
if len(sys.argv) < 2:
    print("Errore: specifica un file PDF.\nUso: pythonXXX buoni_pasto.py <file.pdf>")
    sys.exit(2)

PDF_PATH = sys.argv[1]

if not PDF_PATH.lower().endswith(".pdf"):
    print(f"Errore: il file deve avere estensione .pdf (ricevuto: {PDF_PATH})")
    sys.exit(2)

if not os.path.isfile(PDF_PATH):
    print(f"Errore: file non trovato: {PDF_PATH}")
    sys.exit(2)

# Limite dimensione: 100 KB
size_bytes = os.path.getsize(PDF_PATH)
if size_bytes > 100 * 1024:
    print(f"Errore: file troppo grande ({size_bytes} byte). Dimensione massima consentita: 100 KB.")
    sys.exit(2)

# ------------------ Config ------------------
USE_TABS = False  # True = usa \t; False = spazi allineati

GIORNI = ["lunedì","martedì","mercoledì","giovedì","venerdì","sabato","domenica"]
SOGLIA_PRANZO = time(15, 30)   # ven-sab-dom: >= 15:30
SOGLIA_CENA   = time(20, 30)   # tutti i giorni: >= 20:30

DATE_RE   = re.compile(r"(\d{2}/\d{2}/\d{4})")
ORARIO_RE = re.compile(r"(\d{1,2}[:.,]\d{2})")

# ------------------ Validazione PDF ------------------
def _estrai_righe_testo(pdf):
    righe = []
    for page in pdf.pages:
        txt = page.extract_text() or ""
        righe.extend(line.strip() for line in txt.splitlines())
    return righe

def _conta_righe_giorno(righe):
    count = 0
    esempi = []
    for line in righe:
        if not DATE_RE.search(line):
            continue
        times = ORARIO_RE.findall(line)
        if len(times) >= 2:
            count += 1
            if len(esempi) < 3:
                esempi.append(line)
    return count, esempi

def valida_pdf(path_pdf, min_righe_valide=5):
    report = {
        "righe_valide": 0,
        "esempi": [],
        "etichette_presenti": {},
        "motivi_ok": [],
        "motivi_errore": [],
    }

    if not os.path.isfile(path_pdf):
        report["motivi_errore"].append(f"File non trovato: {path_pdf}")
        return False, report

    try:
        with pdfplumber.open(path_pdf) as pdf:
            if not pdf.pages:
                report["motivi_errore"].append("PDF senza pagine.")
                return False, report

            righe = _estrai_righe_testo(pdf)
            if not any(r.strip() for r in righe):
                report["motivi_errore"].append(
                    "Nessun testo estraibile (PDF potrebbe essere un'immagine scansionata)."
                )
                return False, report
            
            testo_unito = "\n".join(righe).lower()
            
            pres_ing = any(x in testo_unito for x in ["ora ing", "ora ing.", "ingresso"])
            pres_usc = any(x in testo_unito for x in ["ora usc", "ora usc.", "uscita"])
            
            pres_causale = False
            for r in righe:
                lr = r.strip().lower()
                if lr.startswith("tipo") or lr.startswith("lavoro straordinario"):
                    pres_causale = True
                    break
            
            report["etichette_presenti"] = {
                "ingresso": pres_ing,
                "uscita": pres_usc,
                "causale": pres_causale,
            }
            
            if not ((pres_ing and pres_usc) or pres_causale):
                report["motivi_errore"].append(
                    "Etichette chiave non rilevate: "
                    "attese varianti di 'Ingresso', 'Uscita', "
                    "o un'etichetta che inizi con 'Tipo...' o 'Lavoro straordinario...'."
                )
            
            n_validi, esempi = _conta_righe_giorno(righe)
            report["righe_valide"] = n_validi
            report["esempi"] = esempi

            if n_validi == 0:
                report["motivi_errore"].append(
                    "Nessuna riga con 'data + due orari' trovata (layout non riconosciuto)."
                )

            if n_validi < min_righe_valide:
                report["motivi_errore"].append(
                    f"Trovate solo {n_validi} righe valide (< {min_righe_valide})."
                )

            # Ordine colonne (euristica): 'Causale' dopo il secondo orario
            ordine_ok = True
            for line in righe[:200]:
                if "Causale" in line and len(ORARIO_RE.findall(line)) >= 2:
                    it = list(ORARIO_RE.finditer(line))
                    pos_secondo_orario_fine = it[1].end()
                    pos_caus = line.lower().find("causale")
                    if pos_caus != -1 and pos_caus < pos_secondo_orario_fine:
                        ordine_ok = False
                        break
            if not ordine_ok:
                report["motivi_errore"].append("Ordine colonne sospetto: 'Causale' prima degli orari.")

            if report["motivi_errore"]:
                return False, report

            report["motivi_ok"].append(f"Testo estratto da {len(pdf.pages)} pagine.")
            report["motivi_ok"].append(f"Righe 'data + due orari' trovate: {n_validi}.")
            if any(report["etichette_presenti"].values()):
                report["motivi_ok"].append("Etichette chiave rilevate.")
            return True, report

    except Exception as e:
        report["motivi_errore"].append(f"Impossibile aprire/leggere il PDF: {e}")
        return False, report

# ------------------ Parsing & Stampa ------------------
def norm_time(tstr: str) -> time:
    tstr = tstr.replace(",", ":")
    hh, mm = tstr.split(":")
    return time(int(hh), int(mm))

def parse_line(line: str):
    line = line.lstrip("* ").strip()
    m = DATE_RE.search(line)
    if not m:
        return None
    data_str = m.group(1)

    time_matches = list(ORARIO_RE.finditer(line))
    if len(time_matches) < 2:
        return None

    # primi due orari = (ingresso, uscita)
    ora_ing_s = time_matches[0].group(1).replace(",", ":").replace(".", ":")
    ora_usc_s = time_matches[1].group(1).replace(",", ":").replace(".", ":")

    ora_ing = norm_time(ora_ing_s)
    ora_usc = norm_time(ora_usc_s)

    if ora_ing == norm_time("00:00") and ora_usc == norm_time("00:00"):
        return None

    # causale: dopo il 3° orario (se c'è), altrimenti dopo il 2°
    start_idx = time_matches[2].end() if len(time_matches) >= 3 else time_matches[1].end()
    end_idx = time_matches[3].start() if len(time_matches) >= 4 else len(line)
    causale_raw = line[start_idx:end_idx].strip()
    causale = re.sub(r"\s{2,}", " ", causale_raw)
    causale = re.sub(r"^[^\wÀ-ÿ]+|[^\wÀ-ÿ]+$", "", causale).strip()
    if not causale or set(causale) <= {"-"," "}:
        causale = ""

    return data_str, ora_ing, ora_usc, causale

def giorno_settimana(data_str: str) -> str:
    d = datetime.strptime(data_str, "%d/%m/%Y")
    return GIORNI[d.weekday()]

def calcola_pasto(data_str: str, ora_usc: time) -> str | None:
    d = datetime.strptime(data_str, "%d/%m/%Y")
    wd = d.weekday()
    pranzo = (wd in (4, 5, 6)) and (ora_usc >= SOGLIA_PRANZO)
    cena   = (ora_usc >= SOGLIA_CENA)
    if pranzo and cena:
        return "PRANZO e CENA"
    if cena:
        return "CENA"
    if pranzo:
        return "PRANZO"
    return None

def print_table(rows):
    headers = ["Data", "Entrata", "Uscita", "Pranzo/cena", "Nota"]

    if USE_TABS:
        header_line = "\t".join(headers)
        print(header_line)
        print()  # riga vuota tra intestazione e righe
        for r in rows:
            print("\t".join(r))
        return

    # ---- stampa con spazi allineati ----
    if rows:
        cols = list(zip(*([headers] + rows)))
        widths = [max(len(x) for x in col) for col in cols]
    else:
        widths = [len(h) for h in headers]

    header_line = (
        headers[0].ljust(widths[0]) + "  " +
        headers[1].ljust(widths[1]) + " " +
        headers[2].ljust(widths[2]) + " " +
        headers[3].ljust(widths[3]) + "  " +
        headers[4].ljust(widths[4])
    )

    print(header_line)
    print()  # riga vuota tra intestazione e righe

    for r in rows:
        line = (
            r[0].ljust(widths[0]) + "  " +
            r[1].ljust(widths[1]) + " " +
            r[2].ljust(widths[2]) + " " +
            r[3].ljust(widths[3]) + "  " +
            r[4]
        )
        print(line)

def main():
    # ------ VALIDAZIONE ------
    ok, rep = valida_pdf(PDF_PATH)
    if not ok:
        print("Errore: il PDF non ha il formato atteso.")
        for msg in rep["motivi_errore"]:
            print(" -", msg)
        if rep.get("esempi"):
            print("Esempi trovati (prime righe riconosciute):")
            for e in rep["esempi"]:
                print("   >", e)
        sys.exit(2)

    # ------ ELABORAZIONE ------
    risultati = []
    with pdfplumber.open(PDF_PATH) as pdf:
        for page in pdf.pages:
            text = page.extract_text() or ""
            for raw_line in text.splitlines():
                line = raw_line.strip()
                parsed = parse_line(line)
                if not parsed:
                    continue
                data_str, ora_ing, ora_usc, causale = parsed
                if causale == "COMANDO E LOGISTICA":
                    causale = ""
                tipo = calcola_pasto(data_str, ora_usc)
                if not tipo:
                    continue
                wd_it = giorno_settimana(data_str)
                entrata_str = ora_ing.strftime("%H:%M")
                if causale == "RECUPERO COMPENSATIVO" and ora_ing.hour == 7 and ora_ing.minute == 30:
                    # aggiungi asterisco in caso di RECUPERO COMPENSATIVO all'entrata
                    entrata_str = "*07:30"
                risultati.append((
                    f"{data_str} ({wd_it})",
                    entrata_str,
                    f"{ora_usc.strftime('%H:%M')}",
                    tipo,
                    causale
                ))

    print();
    if not risultati:
        print("Nessun buono pasto.")
    else:
        print_table(risultati)
    print();

if __name__ == "__main__":
    main()
