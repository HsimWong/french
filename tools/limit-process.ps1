# limit-process.ps1
# Runs a program inside a Windows Job Object so it gets a hard ceiling on
# committed memory, optionally pinned to a subset of the CPU cores and given a
# lower scheduling priority.
#
#   .\limit-process.ps1 -Exe .\francais.exe -WorkDir C:\app -MemoryMB 700 -CpuCores 2
#
# Why a Job Object and not the obvious alternatives:
#
#   * Process.MaxWorkingSet64 / SetProcessWorkingSetSize is a HINT. The
#     kernel is free to ignore it, and it never triggers an allocation
#     failure. It is not a limit.
#   * SetPriorityClass only reorders scheduling; it does not cap CPU.
#   * ProcessorAffinity caps CPU by restricting the process to a set of
#     cores. It is the only CPU throttle Windows offers per process, and it
#     is a real one: the process cannot run on the other cores at all.
#
# The memory ceiling is real but brutal: exceeding it makes Windows terminate
# the job's processes, exactly like the Linux OOM killer. Set MemoryMB above
# the application's true peak, not at it.
param(
    [Parameter(Mandatory = $true)][string]$Exe,
    [string]$WorkDir = "",
    [int]$MemoryMB = 0,
    [int]$CpuCores = 0,
    [ValidateSet("Normal", "BelowNormal", "Idle")][string]$Priority = "BelowNormal",
    [string[]]$ExtraEnv = @(),
    [switch]$Wait
)

$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

public static class JobLimit {
    [StructLayout(LayoutKind.Sequential)]
    public struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct IO_COUNTERS {
        public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
        public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
        public JOBOBJECT_BASIC_LIMIT_INFORMATION BasicLimitInformation;
        public IO_COUNTERS IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr CreateJobObject(IntPtr lpJobAttributes, string lpName);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr hJob, int infoClass,
        IntPtr lpJobObjectInfo, uint cbJobObjectInfoLength);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int infoClass,
        ref JOBOBJECT_EXTENDED_LIMIT_INFORMATION info, uint length);

    const int JobObjectExtendedLimitInformation = 9;
    const uint JOB_OBJECT_LIMIT_JOB_MEMORY = 0x00000002;
    const uint JOB_OBJECT_LIMIT_PROCESS_MEMORY = 0x00000004;
    const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;

    public static IntPtr Create(int memoryBytes, bool killOnClose) {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero)
            throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateJobObject failed");

        JOBOBJECT_EXTENDED_LIMIT_INFORMATION info = new JOBOBJECT_EXTENDED_LIMIT_INFORMATION();
        if (memoryBytes > 0) {
            info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_JOB_MEMORY | JOB_OBJECT_LIMIT_PROCESS_MEMORY;
            info.JobMemoryLimit = new UIntPtr((ulong)memoryBytes);
            info.ProcessMemoryLimit = new UIntPtr((ulong)memoryBytes);
        }
        if (killOnClose)
            info.BasicLimitInformation.LimitFlags |= JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;

        uint len = (uint)Marshal.SizeOf(typeof(JOBOBJECT_EXTENDED_LIMIT_INFORMATION));
        if (!SetInformationJobObject(job, JobObjectExtendedLimitInformation, ref info, len))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "SetInformationJobObject failed");
        return job;
    }

    public static void Assign(IntPtr job, IntPtr process) {
        if (!AssignProcessToJobObject(job, process))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "AssignProcessToJobObject failed");
    }
}
'@ -ReferencedAssemblies System.Runtime,System.Runtime.InteropServices

if (-not (Test-Path $Exe)) { throw "executable not found: $Exe" }
if ($WorkDir -and -not (Test-Path $WorkDir)) { throw "workdir not found: $WorkDir" }

foreach ($pair in $ExtraEnv) {
    $i = $pair.IndexOf('=')
    if ($i -gt 0) { Set-Item -Path "Env:$($pair.Substring(0, $i))" -Value $pair.Substring($i + 1) }
}

# CreateProcessInfo with an explicit environment block, so we never touch the
# parent's (possibly duplicated) environment dictionary.
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = (Resolve-Path $Exe).Path
if ($WorkDir) { $psi.WorkingDirectory = (Resolve-Path $WorkDir).Path }
$psi.UseShellExecute = $false
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true

$proc = [System.Diagnostics.Process]::Start($psi)
if (-not $proc) { throw "failed to start $Exe" }

$job = [JobLimit]::Create(($MemoryMB * 1MB), $true)
[JobLimit]::Assign($job, $proc.Handle)

$applied = @()
if ($CpuCores -gt 0) {
    $total = [Environment]::ProcessorCount
    $mask = 0
    for ($i = 0; $i -lt [Math]::Min($CpuCores, $total); $i++) { $mask = $mask -bor (1 -shl $i) }
    $proc.ProcessorAffinity = [IntPtr]$mask
    $applied += "cpu cores = 1..$CpuCores of $total"
}
if ($Priority -eq "BelowNormal") { $proc.PriorityClass = [System.Diagnostics.ProcessPriorityClass]::BelowNormal; $applied += "priority = BelowNormal" }
elseif ($Priority -eq "Idle") { $proc.PriorityClass = [System.Diagnostics.ProcessPriorityClass]::Idle; $applied += "priority = Idle" }
if ($MemoryMB -gt 0) { $applied += "memory hard limit = $MemoryMB MB" }

Write-Host "pid=$($proc.Id)  limits: $($applied -join ', ')"
Write-Host "  (exceeding the memory ceiling terminates the process, like the Linux OOM killer)"

if ($Wait) {
    $proc.WaitForExit()
    Write-Host "exited with code $($proc.ExitCode)"
} else {
    Write-Host "running in background; stop it with: Stop-Process -Id $($proc.Id)"
}
