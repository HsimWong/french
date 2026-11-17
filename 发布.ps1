# 双击或在 PowerShell 中执行：.\发布.ps1
# 实际流程见 deploy.ps1；这里只负责打开一个有回显的窗口运行它。
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
Write-Host '正在发布 Français progressif（检查 → 编译 → 切换 → 冒烟测试）…' -ForegroundColor Cyan
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $here 'deploy.ps1')
$code = $LASTEXITCODE
if ($null -eq $code) { $code = 0 }
if ($code -ne 0) {
  Write-Host "发布未成功（退出码 $code），线上版本已回滚/未变动。" -ForegroundColor Red
} else {
  Write-Host '发布成功。' -ForegroundColor Green
}
Write-Host '按任意键关闭窗口…'
$null = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
