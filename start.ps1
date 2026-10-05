$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

Write-Host "正在启动 Claude2API (本地开发测试)..." -ForegroundColor Cyan
& ".\claude2api.exe"