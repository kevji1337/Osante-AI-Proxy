@echo off
setlocal
chcp 65001 >nul
title Osante Proxy

set "PORT=52710"
set "UI_URL=http://127.0.0.1:%PORT%/ui/"
set "BINARY_NAME=osante-proxy.exe"
set "OSANTE_LOG_LEVEL=0"

cls
echo ============================================================
echo                    OSANTE AI PROXY (VERBOSE)
echo ============================================================
echo.

:: 1. Search for Go compiler
set "GO_CMD="
where go >nul 2>&1
if not errorlevel 1 set "GO_CMD=go"
if "%GO_CMD%"=="" if exist "C:\Program Files\Go\bin\go.exe" set "GO_CMD=C:\Program Files\Go\bin\go.exe"
if "%GO_CMD%"=="" if exist "C:\Go\bin\go.exe" set "GO_CMD=C:\Go\bin\go.exe"
if "%GO_CMD%"=="" if exist "%USERPROFILE%\go\bin\go.exe" set "GO_CMD=%USERPROFILE%\go\bin\go.exe"
if "%GO_CMD%"=="" if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "GO_CMD=%LOCALAPPDATA%\Programs\Go\bin\go.exe"

:: 2. If Go compiler found, compile freshest binary
if not "%GO_CMD%"=="" goto do_build
goto check_binary

:do_build
echo [*] Compiling / updating %BINARY_NAME%...
pushd "%~dp0cmd\server"
"%GO_CMD%" build -ldflags="-s -w" -o "%~dp0%BINARY_NAME%" . >nul 2>&1
if errorlevel 1 (
    popd
    echo.
    echo [ERROR] Compilation failed.
    echo.
    pause
    exit /b 1
)
popd
echo [*] Binary is up to date!
echo.
goto run_server

:check_binary
if exist "%~dp0%BINARY_NAME%" (
    echo [*] Running existing %BINARY_NAME% (Go compiler not found)
    echo.
    goto run_server
)
if exist "%~dp0cmd\server\%BINARY_NAME%" (
    echo [*] Running existing cmd\server\%BINARY_NAME% (Go compiler not found)
    echo.
    set "BINARY_NAME=cmd\server\%BINARY_NAME%"
    goto run_server
)

echo [!] %BINARY_NAME% not found and Go is not installed.
echo [!] Please install Go 1.24+ or place %BINARY_NAME% in the root folder.
echo.
pause
exit /b 1

:run_server
echo  * Web UI:      %UI_URL%
echo  * API Base:    http://127.0.0.1:%PORT%/v1
echo  * Storage:     %%USERPROFILE%%\.Osante\osante.db
echo  * Log Level:   DEBUG (Verbose)
echo ============================================================
echo  Starting proxy server... (Press Ctrl+C to stop)
echo ============================================================
echo.

:: Launch browser in background after 2 seconds
start "" cmd /c "timeout /t 2 /nobreak >nul & start %UI_URL%"

:: Run the server
"%~dp0%BINARY_NAME%"

echo.
echo ============================================================
echo [!] Server stopped.
echo ============================================================
pause