@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

echo ============================================================
echo   DSH Desktop Packager
echo   version input -^> update version info -^> rebuild resources -^> build DSH.exe
echo ============================================================
echo.

rem ---- read current version from winres.json ----
powershell -NoProfile -ExecutionPolicy Bypass -Command "[IO.File]::WriteAllText('ver.tmp',[regex]::Match([IO.File]::ReadAllText('winres.json'),'\d+\.\d+\.\d+\.\d+').Value)"
set "CURVER="
if exist ver.tmp (
  set /p CURVER=<ver.tmp
  del ver.tmp
)
if defined CURVER echo Current version: %CURVER%
echo.

set "VERSION="
set /p VERSION=Enter new version (e.g. 1.0.3, empty=keep current):

if not defined VERSION goto no_version

rem strip optional v prefix and validate 1.0.3 format
set "V2=!VERSION:v=!"
echo !V2!| findstr /r "^[0-9][0-9]*[.][0-9][0-9]*[.][0-9][0-9]*$" >nul
if errorlevel 1 goto bad_version
set "VERSION=!V2!"

echo [1/4] Updating version to !VERSION! ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$v='!VERSION!';$q=[char]34;$w='winres.json';[IO.File]::WriteAllText($w,[regex]::Replace([IO.File]::ReadAllText($w),'\d+\.\d+\.\d+\.\d+',$v+'.0'),(New-Object Text.UTF8Encoding($false)));$u='updater.go';[IO.File]::WriteAllText($u,[regex]::Replace([IO.File]::ReadAllText($u),'const appVersion = \S+','const appVersion = '+$q+$v+$q),(New-Object Text.UTF8Encoding($false)));Write-Host ('version updated: '+$v)"
if errorlevel 1 goto fail_ver
goto go_step

:no_version
echo [1/4] Keeping current version
goto go_step

:bad_version
echo [ERROR] Invalid version format. Example: 1.0.3
goto fail

:fail_ver
echo [ERROR] Failed to update version
goto fail

:go_step
rem ---- 2. rebuild Windows resources (icon/manifest/version) ----
set "WINRES="
where go-winres >nul 2>nul && set "WINRES=go-winres"
if not defined WINRES if exist "%~dp0..\tools\bin\go-winres.exe" set "WINRES=%~dp0..\tools\bin\go-winres.exe"
if not defined WINRES if exist "%~dp0tools\bin\go-winres.exe" set "WINRES=%~dp0tools\bin\go-winres.exe"
if not defined WINRES goto no_winres
echo [2/4] Regenerating Windows resources ...
"%WINRES%" make --in winres.json --arch amd64
if errorlevel 1 goto fail_winres
goto go_find

:no_winres
echo [2/4] go-winres not found; keeping existing rsrc_windows_amd64.syso (version info will NOT be updated)
goto go_find

:fail_winres
echo [ERROR] Failed to generate resources
goto fail

:go_find
rem ---- 3. locate Go toolchain ----
set "GOEXE="
if defined DSH_GO if exist "%DSH_GO%" set "GOEXE=%DSH_GO%"
if not defined GOEXE (where go >nul 2>nul && set "GOEXE=go")
if not defined GOEXE if exist "%~dp0tools\go\bin\go.exe" set "GOEXE=%~dp0tools\go\bin\go.exe"
if not defined GOEXE if exist "%~dp0..\tools\go\bin\go.exe" set "GOEXE=%~dp0..\tools\go\bin\go.exe"
if not defined GOEXE if exist "%ProgramFiles%\Go\bin\go.exe" set "GOEXE=%ProgramFiles%\Go\bin\go.exe"
if not defined GOEXE if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "GOEXE=%LOCALAPPDATA%\Programs\Go\bin\go.exe"
if not defined GOEXE goto no_go
set "DSH_GO=%GOEXE%"
echo [3/4] Using Go: %GOEXE%
goto build

:no_go
echo [ERROR] Go toolchain not found. Install Go or set DSH_GO to go.exe
goto fail

:build
rem ---- 4. build ----
echo [4/4] Building DSH.exe ...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
if errorlevel 1 goto fail_build

if exist "%~dp0runtime.zip" (
  echo runtime.zip found, building full version DSH-full.exe ...
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" -Full
  if errorlevel 1 goto fail_build
)

echo.
echo ============================================================
echo   Build OK!
for %%f in ("%~dp0DSH.exe") do echo   Output: %%~nxf  (%%~zf bytes)
if exist "%~dp0DSH-full.exe" for %%f in ("%~dp0DSH-full.exe") do echo   Output: %%~nxf  (%%~zf bytes)
echo ============================================================
echo.
goto done

:fail_build
echo [ERROR] Build failed
goto fail

:fail
echo.
pause
exit /b 1

:done
pause
