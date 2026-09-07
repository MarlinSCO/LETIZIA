# Letizia - sorgente pulito e cross-platform

Questa edizione non contiene dati di magazzino iniziali.

Al primo avvio:
- nessun articolo;
- nessuna giacenza;
- nessun carico/scarico;
- nessuno storico mensile;
- viene creato soltanto il mese corrente, vuoto, necessario al funzionamento.

Il file dati `MagazzinoDati.json` NON è incluso nel progetto. Viene creato automaticamente al primo avvio.

## Ubuntu

Con Go installato:

```bash
chmod +x AVVIO_UBUNTU.sh COMPILA_UBUNTU.sh
./AVVIO_UBUNTU.sh
```

oppure, per creare l'eseguibile Linux portabile:

```bash
./COMPILA_UBUNTU.sh
./Letizia
```

Il programma apre l'interfaccia nel browser predefinito usando `xdg-open` o `gio` e lavora solo su `127.0.0.1` (computer locale).

## Windows

Con Go installato, eseguire `COMPILA_WINDOWS.bat` oppure:

```powershell
go build -trimpath -ldflags="-s -w" -o Letizia.exe .
```

## Compilare Windows da Ubuntu

```bash
./COMPILA_WINDOWS_DA_UBUNTU.sh
```

## Dati utente

Su Ubuntu i dati vengono salvati nella stessa cartella usata dalla versione precedente, per non perdere i dati:

```text
~/.local/share/MagazzinoPortatile/MagazzinoDati.json
```

Se è definita la variabile `XDG_DATA_HOME`, viene usata `$XDG_DATA_HOME/MagazzinoPortatile/`. Il nome interno della cartella dati resta volutamente invariato per compatibilità.

Su Windows viene usata la cartella di configurazione dell'utente. In questo modo l'eseguibile può essere aggiornato senza cancellare i dati.

Per ricominciare completamente da zero, chiudere il programma e cancellare `MagazzinoDati.json` dalla cartella dati dell'utente.

## Logica mensile

- I dati di ogni mese restano memorizzati quando si cambia mese.
- Creando il mese successivo, la giacenza finale del mese precedente diventa la GIACENZA iniziale del nuovo mese.
- CARICO e SCARICO del nuovo mese partono da zero.
- Tornando a un mese già esistente, vengono ricaricati i suoi valori originali.

## Dipendenze

Solo libreria standard Go: nessun pacchetto esterno, nessun database, nessun Excel, nessun ActiveX.

## FILE DB

Per chi volesse eseguire il software direttamente scaricare il file .deb e installarlo.
Successivamente provvederò a fornirVi il file .exe per Windows.
Accetto consigli, critiche e idee. 
