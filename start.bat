@echo off
setlocal EnableExtensions
chcp 65001 >nul 2>&1
title Osante Proxy

:: ------------------------------------------------------------------
:: Osante Proxy launcher.
::
:: Defaults are deliberately non-invasive: the port and the log level
:: stored in the database (and edited in the web UI) win unless you
:: override them here with --port / --debug. The previous version always
:: exported OSANTE_LOG_LEVEL=0, silently forcing DEBUG on every run.
:: ------------------------------------------------------------------

set "PORT=12710"
set "BINARY_NAME=osante-proxy.exe"
set "SCRIPT_DIR=%~dp0"
set "SERVER_DIR=%SCRIPT_DIR%cmd\server"
set "BIN_PATH=%SERVER_DIR%\%BINARY_NAME%"
set "DO_BUILD=1"
set "OPEN_UI=1"
set "EXTRA_ARGS="

:parse_args
if "%~1"=="" goto args_done
if /i "%~1"=="--help"     goto usage
if /i "%~1"=="-h"         goto usage
if /i "%~1"=="--debug"    set "OSANTE_LOG_LEVEL=0" & shift & goto parse_args
if /i "%~1"=="--no-build" set "DO_BUILD=0"         & shift & goto parse_args
if /i "%~1"=="--no-ui"    set "OPEN_UI=0"          & shift & goto parse_args
if /i "%~1"=="--port"     set "PORT=%~2" & set "OSANTE_PORT=%~2" & shift & shift & goto parse_args
:: Anything else is forwarded to the server binary verbatim.
set "EXTRA_ARGS=%EXTRA_ARGS% %1"
shift
goto parse_args

:usage
echo Usage: start.bat [--debug] [--no-build] [--no-ui] [--port N] [-- server flags]
echo.
echo   --debug      force OSANTE_LOG_LEVEL=0 (DEBUG) for this run
echo   --no-build   run the existing binary without recompiling
echo   --no-ui      do not open the web UI in a browser
echo   --port N     override the configured port (sets OSANTE_PORT)
echo.
echo Without --port / --debug the port and log level saved in the database
echo (Settings in the web UI) are used.
endlocal
exit /b 0

:args_done
:: An OSANTE_PORT inherited from the environment still decides where to probe.
if defined OSANTE_PORT set "PORT=%OSANTE_PORT%"
set "UI_URL=http://127.0.0.1:%PORT%/ui/"

cls
echo ============================================================
echo                      OSANTE AI PROXY
echo ============================================================
echo.

:: Is something already listening on that port? Starting a second instance
:: would just fail on bind, so treat it as "already running" and show the UI.
netstat -ano | findstr /c:":%PORT% " | findstr /i "LISTENING" >nul 2>&1
if not errorlevel 1 goto already_running

:: Windows hands out 100-port blocks inside the dynamic range (49152-65535) to
:: Hyper-V / WSL / Docker NAT. Binding a port inside one of those blocks fails
:: with "An attempt was made to access a socket in a way forbidden by its access
:: permissions" (WSAEACCES) even though netstat shows nothing listening, and the
:: blocks move around between reboots. Detect it before starting the server.
set "PORT_RESERVED="
for /f "tokens=1,2" %%a in ('netsh int ipv4 show excludedportrange protocol^=tcp 2^>nul ^| findstr /r /c:"^ *[0-9][0-9]*  *[0-9][0-9]*"') do (
    if %PORT% GEQ %%a if %PORT% LEQ %%b set "PORT_RESERVED=%%a-%%b"
)
if defined PORT_RESERVED goto port_reserved

:: Find a Go compiler; without one we can still run a prebuilt binary.
set "GO_CMD="
where go >nul 2>&1
if not errorlevel 1 set "GO_CMD=go"
if not defined GO_CMD if exist "C:\Program Files\Go\bin\go.exe" set "GO_CMD=C:\Program Files\Go\bin\go.exe"
if not defined GO_CMD if exist "C:\Go\bin\go.exe" set "GO_CMD=C:\Go\bin\go.exe"
if not defined GO_CMD if exist "%USERPROFILE%\go\bin\go.exe" set "GO_CMD=%USERPROFILE%\go\bin\go.exe"
if not defined GO_CMD if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "GO_CMD=%LOCALAPPDATA%\Programs\Go\bin\go.exe"

if "%DO_BUILD%"=="0" goto check_binary
if not defined GO_CMD goto check_binary

echo [*] Compiling %BINARY_NAME% in %SERVER_DIR%...
pushd "%SERVER_DIR%"
"%GO_CMD%" build -ldflags="-s -w" -o "%BINARY_NAME%" .
if errorlevel 1 (
    popd
    echo.
    echo [ERROR] Build failed. See messages above.
    echo.
    pause
    endlocal
    exit /b 1
)
popd
echo [*] Binary updated.
echo.
goto run_server

:check_binary
if exist "%BIN_PATH%" (
    if "%DO_BUILD%"=="0" (
        echo [*] Skipping build ^(--no-build^), running existing %BINARY_NAME%.
    ) else (
        echo [*] Go compiler not found, running existing %BINARY_NAME%.
    )
    echo.
    goto run_server
)

echo [!] %BINARY_NAME% not found and Go is not installed.
echo [!] Install Go 1.27+ or place %BINARY_NAME% in %SERVER_DIR%
echo.
pause
endlocal
exit /b 1

:port_reserved
echo [!] Port %PORT% is inside a Windows reserved port range (%PORT_RESERVED%).
echo [!] Nothing is listening on it, but binding it fails with
echo [!] "socket in a way forbidden by its access permissions" (WSAEACCES).
echo [!]
echo [!] These ranges are handed to Hyper-V / WSL / Docker NAT and move between
echo [!] reboots. Pick one of:
echo [!]
echo [!]   1. Run on another port for now:
echo [!]        start.bat --port 51710
echo [!]
echo [!]   2. Move the proxy below the dynamic range (49152) for good - set the
echo [!]      port in the web UI (Settings) or run: start.bat --port 12710
echo [!]      Remember to point ANTHROPIC_BASE_URL at the new port.
echo [!]
echo [!]   3. Free this exact port (needs an Administrator prompt):
echo [!]        net stop winnat
echo [!]        netsh int ipv4 add excludedportrange protocol=tcp startport=%PORT% numberofports=1 store=persistent
echo [!]        net start winnat
echo [!]
echo [!] Current reservations: netsh int ipv4 show excludedportrange protocol=tcp
echo.
pause
endlocal
exit /b 1

:already_running
echo [*] Port %PORT% is already in use - an instance appears to be running.
if "%OPEN_UI%"=="1" (
    echo [*] Opening %UI_URL%
    start "" "%UI_URL%"
) else (
    echo [*] Web UI: %UI_URL%
)
endlocal
exit /b 0

:run_server
echo  * Web UI:      %UI_URL%
echo  * API Base:    http://127.0.0.1:%PORT%/v1
echo  * Storage:     %USERPROFILE%\.Osante\osante.db
if defined OSANTE_LOG_LEVEL echo  * Log Level:   %OSANTE_LOG_LEVEL% ^(overriding the saved setting^)
if not defined OSANTE_LOG_LEVEL echo  * Log Level:   from database ^(use --debug to force DEBUG^)
echo ============================================================
echo  Starting proxy server... (Press Ctrl+C to stop)
echo ============================================================
echo.

:: Wait for /health to answer before opening the browser, instead of guessing
:: with a fixed 2s sleep. If the server never comes up (or listens on a
:: different port than we probed), no window is opened.
if "%OPEN_UI%"=="1" start "" /b powershell -NoProfile -WindowStyle Hidden -Command "for($i=0;$i -lt 40;$i++){ try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 'http://127.0.0.1:%PORT%/health'; if ($r.StatusCode -eq 200) { Start-Process '%UI_URL%'; exit 0 } } catch {}; Start-Sleep -Milliseconds 500 }"

pushd "%SERVER_DIR%"
"%BIN_PATH%"%EXTRA_ARGS%
set "EXIT_CODE=%ERRORLEVEL%"
popd

echo.
echo ============================================================
echo [!] Server stopped (exit code %EXIT_CODE%).
echo ============================================================
if not "%EXIT_CODE%"=="0" pause
endlocal
exit /b %EXIT_CODE%
