@echo off
rem Rebuild and run this checkout, forwarding all arguments to the game.
setlocal DisableDelayedExpansion
pushd "%~dp0.." || exit /b 1
go build -o build/dev/nanolathe.exe ./cmd/nanolathe
if errorlevel 1 goto done
build\dev\nanolathe.exe %*
:done
set status=%errorlevel%
popd
exit /b %status%
