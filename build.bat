@echo off
echo Building Content Node...

REM Windows build
echo Building for Windows...
go build -o .build/windows.exe ./cmd
if %errorlevel% neq 0 (
    echo Windows build failed!
    exit /b %errorlevel%
)
echo Windows build successful: .build/windows.exe

echo.
echo Copying .env...
copy .env ".build/.env" >nul
if %errorlevel% neq 0 (
    echo Warning: Failed to copy .env file!
) else (
    echo Copied .env successfully
)

echo.
echo All builds completed successfully!
