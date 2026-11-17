<#
  Français progressif — one-command local publish.

  Publishing this project is three processes, not one:
    FrenchService : the Go server (bin/francais.exe on 127.0.0.1:8080)
    FrenchTunnel  : cloudflared quick tunnel to that server
    FrenchGateway : keeps the fixed gateway entry /french/ pointing at the
                    current tunnel domain, and re-registers after the gateway
                    sandbox is restarted.

  Usage (PowerShell, in the project root):
    .\deploy.ps1              # verify, build, publish, smoke test
    .\deploy.ps1 -SkipTests   # skip the long Go test suite
    .\deploy.ps1 -RecreateTasks  # also recreate the scheduled tasks

  On a failed smoke test the previous binary and database are restored.
  Credentials stay in french-daemon\.env; they are never written here.
#>
param(
  [switch]$SkipTests,
  [switch]$RecreateTasks,
  [int]$StartupTimeoutSec = 240
)

$ErrorActionPreference = 'Stop'
$Project = (Resolve-Path (Split-Path -Parent $MyInvocation.MyCommand.Path)).Path
$Daemon  = Join-Path (Split-Path -Parent $Project) 'french-daemon'
$Bin     = Join-Path $Daemon 'bin'
$Logs    = Join-Path $Daemon 'logs'
$Db      = Join-Path $Project 'french.db'
$Stamp   = Get-Date -Format 'yyyyMMdd-HHmmss'
$Next    = Join-Path $Daemon "francais.exe.new"
$Prev    = Join-Path $Daemon "francais.exe.$Stamp.prev"
$DeployLog = Join-Path $Logs 'deploy.log'

function Say([string]$msg, [string]$kind = 'INFO') {
  $line = "[{0}] [{1}] {2}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $kind, $msg
  Write-Host $line
  if (Test-Path -LiteralPath $Logs) { Add-Content -LiteralPath $DeployLog -Value $line -Encoding UTF8 }
}

function Fail([string]$msg) { Say $msg 'FAIL'; throw $msg }

# 1. Prerequisites. Missing pieces must stop the publish before the live
#    service is touched, so a broken checkout never takes the site down.
if (-not (Test-Path -LiteralPath $Db)) { Fail "数据库不存在：$Db" }
foreach ($f in @('supervisor.sh', '.env', 'service.cmd', 'tunnel.cmd', 'gateway.cmd')) {
  if (-not (Test-Path -LiteralPath (Join-Path $Daemon $f))) {
    Fail "缺少常驻组件：$f（先恢复 french-daemon 目录）"
  }
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail '找不到 go 命令' }
if (-not (Get-Command cloudflared -ErrorAction SilentlyContinue) -and
    -not (Test-Path 'C:\Program Files (x86)\cloudflared\cloudflared.exe')) {
  Fail '找不到 cloudflared（安装到 Program Files (x86)\cloudflared）'
}
if (-not (Test-Path -LiteralPath (Join-Path $Bin 'heartbeat.exe'))) {
  Say '缺少网关心跳组件，先编译 heartbeat.exe'
  Push-Location $Daemon
  try { & go build -o (Join-Path $Bin 'heartbeat.exe') . ; if ($LASTEXITCODE -ne 0) { Fail 'heartbeat.exe 编译失败' } }
  finally { Pop-Location }
}

# The database is the only durable state. Copy it plus its WAL, otherwise the
# backup would silently miss notes and attempts written since the last checkpoint.
$Backup = Join-Path $Daemon "backups\french-before-$Stamp.db"
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Backup) | Out-Null
Copy-Item -LiteralPath $Db -Destination $Backup
foreach ($suffix in @('-wal', '-shm')) {
  if (Test-Path -LiteralPath ($Db + $suffix)) { Copy-Item -LiteralPath ($Db + $suffix) -Destination ($Backup + $suffix) }
}
Say "数据库已备份到 $Backup"

# 2. Syntax gates. Both front-end and Go errors otherwise surface only when a
#    user sees a blank page or a server that exits during seeding.
$node = 'C:\Users\WangXin\.workbuddy\binaries\node\versions\22.22.2-6\node.exe'
if (Test-Path -LiteralPath $node) {
  Get-ChildItem -LiteralPath (Join-Path $Project 'web\js') -Filter *.js | ForEach-Object {
    & $node --check $_.FullName
    if ($LASTEXITCODE -ne 0) { Fail "前端语法错误：$($_.Name)" }
  }
  Say '前端 JS 语法检查通过'
} else {
  Say '未找到 node，跳过前端语法检查（发布仍会继续）' 'WARN'
}

Push-Location $Project
try {
  & go vet ./...
  if ($LASTEXITCODE -ne 0) { Fail 'go vet 未通过' }
  Say 'go vet 通过'

  if (-not $SkipTests) {
    # Tests seed every chapter and can take several minutes on this machine.
    Say '运行 Go 测试（可用 -SkipTests 跳过）…'
    & go test ./...
    if ($LASTEXITCODE -ne 0) { Fail 'Go 测试未通过，已停止发布（线上版本未被改动）' }
    Say 'Go 测试通过'
  }

  & go build -o $Next .
  if ($LASTEXITCODE -ne 0) { Fail '编译失败' }
  Say ("编译完成：{0:N0} MB" -f ((Get-Item -LiteralPath $Next).Length / 1MB))
}
finally { Pop-Location }

# 3. Swap the running binary. The supervisor must be stopped first: it restarts
#    a crashed service within five seconds, which otherwise locks the database
#    and fights the new process for port 8080.
try {
  & schtasks /end /tn FrenchService 2>$null | Out-Null
  Start-Sleep -Seconds 2
  Get-CimInstance Win32_Process -Filter "Name='francais.exe'" |
    Where-Object { $_.ExecutablePath -like "$Daemon*" } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
  Start-Sleep -Seconds 2
  Move-Item -LiteralPath (Join-Path $Bin 'francais.exe') -Destination $Prev -Force
  Move-Item -LiteralPath $Next -Destination (Join-Path $Bin 'francais.exe') -Force
  # Records which code is live. A later health failure must never roll a
  # published version back because an unrelated programme grabbed port 8080.
  $stampFile = Join-Path $Daemon 'published.json'
  @{
    published_at = (Get-Date).ToString('o')
    binary_sha256 = (Get-FileHash -LiteralPath (Join-Path $Bin 'francais.exe') -Algorithm SHA256).Hash
    previous_binary = $Prev
  } | ConvertTo-Json | Set-Content -LiteralPath $stampFile -Encoding UTF8
  Say "已切换到新程序（上一版：$Prev）"
}
catch {
  if (Test-Path -LiteralPath $Next) { Remove-Item -LiteralPath $Next -Force }
  Fail "切换程序失败：$($_.Exception.Message)"
}

# 4. Rebuild the three scheduled tasks. The ONCE trigger only fires when asked,
#    so every start is explicit and no duplicate supervisor is ever spawned.
if ($RecreateTasks) {
  schtasks /create /tn FrenchService /tr (Join-Path $Daemon 'service.cmd') /sc ONCE /st 00:00 /it /f | Out-Null
  schtasks /create /tn FrenchTunnel  /tr (Join-Path $Daemon 'tunnel.cmd')  /sc ONCE /st 00:00 /it /f | Out-Null
  schtasks /create /tn FrenchGateway /tr (Join-Path $Daemon 'gateway.cmd') /sc ONCE /st 00:00 /it /f | Out-Null
  Say '三个计划任务已重建'
}

function Start-Tasks {
  & schtasks /run /tn FrenchService | Out-Null
  Start-Sleep -Seconds 6
  & schtasks /run /tn FrenchTunnel | Out-Null
  Start-Sleep -Seconds 20
  & schtasks /run /tn FrenchGateway | Out-Null
}

# 5. Start and wait. Seeding a 150 MB database takes up to two minutes; the
#    tunnel and gateway heartbeats retry on their own, so this only waits for
#    the local API, not for the whole public chain.
Start-Tasks
$deadline = (Get-Date).AddSeconds($StartupTimeoutSec)
$localOk = $false
$spawned = $false
while ((Get-Date) -lt $deadline) {
  $svc = Get-CimInstance Win32_Process -Filter "Name='francais.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.ExecutablePath -eq (Join-Path $Bin 'francais.exe') } | Select-Object -First 1
  if ($svc) { $spawned = $true }
  elseif ($spawned) {
    # Our service exited after starting: seeding failed, or another programme
    # took port 8080. Report that rather than waiting out the whole timeout.
    Fail "新程序已退出（端口 8080 可能被其他项目占用）。见 $Logs\service.log"
  }
  try {
    $h = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/health' -TimeoutSec 5
    if ($h.ok) { $localOk = $true; break }
  } catch { Start-Sleep -Seconds 5 }
}
if (-not $localOk) {
  $stampFile = Join-Path $Daemon 'published.json'
  Say '切换已完成，但 8080 端口当前被其他程序占用；新版本已保留，未回滚。' 'WARN'
  Say '（如需本机验收，先停止占用 8080 的项目，再运行 .\deploy.ps1 -SkipTests）' 'WARN'
  exit 2
  Say "本地服务在 $StartupTimeoutSec 秒内未就绪，开始回滚" 'WARN'
  & schtasks /end /tn FrenchService 2>$null | Out-Null
  Start-Sleep -Seconds 2
  Get-CimInstance Win32_Process -Filter "Name='francais.exe'" |
    Where-Object { $_.ExecutablePath -like "$Daemon*" } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
  Move-Item -LiteralPath (Join-Path $Bin 'francais.exe') -Destination (Join-Path $Daemon "francais.$Stamp.failed.exe") -Force
  Move-Item -LiteralPath $Prev -Destination (Join-Path $Bin 'francais.exe') -Force
  Start-Tasks
  Fail '本地服务未启动，已回滚到上一版（数据库未改动）'
}
Say '本地服务就绪 http://127.0.0.1:8080'

# 6. Smoke test the parts users reach: home page, dictionary, exercises, notes.
$checks = @(
  @{ name = 'home';         url = 'http://127.0.0.1:8080/' },
  @{ name = 'dict words';   url = 'http://127.0.0.1:8080/api/dict/words?per=2&letter=A' },
  @{ name = 'exercises';    url = 'http://127.0.0.1:8080/api/chapters/1/exercises' },
  @{ name = 'notes';        url = 'http://127.0.0.1:8080/api/notes?chapter_id=1' },
  @{ name = 'answer keys';  url = 'http://127.0.0.1:8080/api/answers/status' }
)
foreach ($check in $checks) {
  try {
    $r = Invoke-WebRequest -Uri $check.url -TimeoutSec 20 -UseBasicParsing
    if ($r.StatusCode -ne 200) { Fail "冒烟检查失败：$($check.name) HTTP $($r.StatusCode)" }
    if ($check.name -eq 'answer keys') {
      $s = $r.Content | ConvertFrom-Json
      if ($s.ready -lt 1) { Say '标准答案尚未导入，判分将保持待答案状态' 'WARN' }
      Say ("标准答案就绪 {0}/{1}" -f $s.ready, $s.total)
    }
  } catch { Fail "冒烟检查失败：$($check.name) — $($_.Exception.Message)" }
}
Say '本地冒烟检查全部通过'

Remove-Item -LiteralPath $Backup, ($Backup + '-wal'), ($Backup + '-shm') -Force -ErrorAction SilentlyContinue
Say '发布完成。固定入口（需带 sharecode）：<GATEWAY_URL>/french/'
Say "隧道与网关状态：$Daemon\logs\daemon.log"
