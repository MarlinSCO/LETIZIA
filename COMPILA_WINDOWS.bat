@echo off
cd /d "%~dp0"
go test ./...
if errorlevel 1 pause & exit /b 1
go build -trimpath -ldflags="-s -w" -o Letizia.exe .
if errorlevel 1 pause & exit /b 1
echo Creato: Letizia.exe
pause
