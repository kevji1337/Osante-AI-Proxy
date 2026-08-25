@echo off
setlocal EnableExtensions
chcp 65001 >nul 2>&1

:: ------------------------------------------------------------------
:: Osante Proxy launcher, background variant.
::
:: Builds the GUI-subsystem binary (osante-proxyw.exe, -H=windowsgui) and
:: starts it detached, so there is no console window at all — everything
:: happens in the web UI, including stopping the server again via the
:: dashboard's SHUT DOWN button.
::
:: Because the process has no console, it also has nowhere to print: use the
:: Logs tab (or GET /api/logs) to read the log, and --debug-file to capture
:: request bodies.
:: ------------------------------------------------------------------

set "PORT=12710"
set "BINARY_NAME=osante-proxyw.exe"
set "SCRIPT_DIR=%~dp0"
set "SERVER_DIR=%SCRIPT_DIR%cmd\server"
set "BIN_PATH=%SERVER_DIR%\%BINARY_NAME%"
set "DO_BUILD=1"
set "OPEN_UI=1"

:parse_args
if "%~1"=="" goto args_done
if /i "%~1"=="--help"       goto usage
if /i "%~1"=="-h"           goto usage
if /i "%~1"=="--debug"      set "OSANTE_LOG_LEVEL=0"        & shift & goto parse_args
if /i "%~1"=="--debug-file" set "OSANTE_DEBUG_FILE=%~2"     & shift & shift & goto parse_args
if /i "%~1"=="--no-build"   set "DO_BUILD=0"                & shift & goto parse_args
if /i "%~1"=="--no-ui"      set "OPEN_UI=0"                 & shift & goto parse_args
if /i "%~1"=="--port"       set "PORT=%~2" & set "OSANTE_PORT=%~2" & shift & shift & goto parse_args
if /i "%~1"=="--stop"       goto stop_server
shift
goto parse_args

:usage
echo Usage: start-background.bat [--debug] [--debug-file PATH] [--no-build]
echo                             [--no-ui] [--port N] [--stop]
echo.
echo Starts the proxy with no console window. Manage it from the web UI; the
echo dashboard's SHUT DOWN button stops it.
echo.
echo   --stop            ask a running instance to shut down, then exit
echo   --debug           force OSANTE_LOG_LEVEL=0 (DEBUG) for this run
echo   --debug-file PATH record full request/response bodies to PATH
echo   --no-build        run the existing binary without recompiling
echo   --no-ui           do not open the web UI in a browser
echo   --port N          override the configured port (sets OSANTE_PORT)
endlocal
exit /b 0

:args_done
if defined OSANTE_PORT set "PORT=%OSANTE_PORT%"
set "UI_URL=http://127.0.0.1:%PORT%/ui/"

echo ============================================================
echo             OSANTE AI PROXY (background)
echo ============================================================
echo.

:: Already running? Just surface the UI.
netstat -ano | findstr /c:":%PORT% " | findstr /i "LISTENING" >nul 2>&1
if not errorlevel 1 (
    echo [*] Already running on port %PORT%.
    if "%OPEN_UI%"=="1" start "" "%UI_URL%"
    endlocal
    exit /b 0
)

:: Windows reserves shifting 100-port blocks inside the dynamic range for
:: Hyper-V / WSL / Docker NAT; binding one fails with WSAEACCES even though
:: nothing is listening. Catch it here rather than in a log nobody is watching.
set "PORT_RESERVED="
for /f "tokens=1,2" %%a in ('netsh int ipv4 show excludedportrange protocol^=tcp 2^>nul ^| findstr /r /c:"^ *[0-9][0-9]*  *[0-9][0-9]*"') do (
    if %PORT% GEQ %%a if %PORT% LEQ %%b set "PORT_RESERVED=%%a-%%b"
)
if defined PORT_RESERVED (
    echo [!] Port %PORT% is inside a Windows reserved range ^(%PORT_RESERVED%^).
    echo [!] Pick another with --port, e.g. start-background.bat --port 12711
    echo.
    pause
    endlocal
    exit /b 1
)

if "%DO_BUILD%"=="0" goto check_binary

set "GO_CMD="
where go >nul 2>&1
if not errorlevel 1 set "GO_CMD=go"
if not defined GO_CMD if exist "C:\Program Files\Go\bin\go.exe" set "GO_CMD=C:\Program Files\Go\bin\go.exe"
if not defined GO_CMD if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "GO_CMD=%LOCALAPPDATA%\Programs\Go\bin\go.exe"
if not defined GO_CMD goto check_binary

echo [*] Building %BINARY_NAME% ^(no console window^)...
pushd "%SERVER_DIR%"
:: -H=windowsgui puts the binary in the GUI subsystem: Windows starts it without
:: allocating a console. It has no stdout as a result — the log lives in the ring
:: buffer behind /api/logs.
"%GO_CMD%" build -ldflags="-s -w -H=windowsgui" -o "%BINARY_NAME%" .
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

:check_binary
if not exist "%BIN_PATH%" (
    echo [!] %BINARY_NAME% not found and it could not be built.
    echo [!] Install Go 1.27+ or run start.bat instead ^(console mode^).
    echo.
    pause
    endlocal
    exit /b 1
)

echo [*] Starting detached on port %PORT%...
:: No /b: with it the child inherits this console's handles and the launcher blocks
:: until the server exits, which defeats the point. The binary is a GUI-subsystem
:: build, so no window appears either way.
pushd "%SERVER_DIR%"
:: Redirect the child's inherited stdio to NUL as well: a detached process still
:: holds this console's handles, which keeps any pipe attached to the launcher open
:: and makes callers think the launcher never finished.
start "" "%BIN_PATH%" >nul 2>&1
popd

:: Wait for the server to answer before opening the browser; if it never comes
:: up, say so instead of opening a dead tab.
set "READY="
for /l %%i in (1,1,20) do (
    if not defined READY (
        curl -s -m 2 -o nul "http://127.0.0.1:%PORT%/health" >nul 2>&1
        if not errorlevel 1 (
            set "READY=1"
        ) else (
            rem ~1s between probes; ping is the portable sleep in cmd.
            ping -n 2 127.0.0.1 >nul 2>&1
        )
    )
)

if not defined READY (
    echo [!] The server did not answer on port %PORT%.
    echo [!] Check the log: %USERPROFILE%\.Osante\  or run start.bat to see console output.
    echo.
    pause
    endlocal
    exit /b 1
)

echo.
echo  * Web UI:      %UI_URL%
echo  * API Base:    http://127.0.0.1:%PORT%/v1
echo  * Storage:     %USERPROFILE%\.Osante\osante.db
if defined OSANTE_DEBUG_FILE echo  * Debug log:   %OSANTE_DEBUG_FILE% ^(records full request bodies^)
echo ============================================================
echo  Running in the background. No console window.
echo  Stop it from the dashboard's SHUT DOWN button, or:
echo    start-background.bat --stop
echo ============================================================
if "%OPEN_UI%"=="1" start "" "%UI_URL%"
endlocal
exit /b 0

:stop_server
if defined OSANTE_PORT set "PORT=%OSANTE_PORT%"
echo [*] Asking the server on port %PORT% to shut down...
curl -s -X POST -H "Content-Type: application/json" "http://127.0.0.1:%PORT%/api/actions/shutdown"
echo.
endlocal
exit /b 0
