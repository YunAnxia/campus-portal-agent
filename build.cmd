@echo off
rem portalagent build script (batch version, immune to PowerShell execution policy)
setlocal
cd /d "%~dp0"
set CGO_ENABLED=0

echo --- gofmt ---
set UNFMT=0
for /f "delims=" %%i in ('gofmt -l .') do (
    echo [WARN] not formatted: %%i
    set UNFMT=1
)
if "%UNFMT%"=="0" echo all formatted

echo --- go vet ---
go vet ./...
if errorlevel 1 goto fail

echo --- go test ---
go test ./...
if errorlevel 1 goto fail

echo --- go build ---
go build -trimpath -ldflags "-s -w" -o portalagent.exe .
if errorlevel 1 goto fail

echo.
echo built: %CD%\portalagent.exe
exit /b 0

:fail
echo.
echo BUILD FAILED
exit /b 1
