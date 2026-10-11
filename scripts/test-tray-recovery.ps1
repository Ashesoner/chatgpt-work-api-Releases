param(
    [int]$WaitMilliseconds = 1200,
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public static class CWapiTrayTestNative
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NOTIFYICONDATA
    {
        public uint cbSize;
        public IntPtr hWnd;
        public uint uID;
        public uint uFlags;
        public uint uCallbackMessage;
        public IntPtr hIcon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string szTip;
        public uint dwState;
        public uint dwStateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string szInfo;
        public uint uTimeoutOrVersion;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)] public string szInfoTitle;
        public uint dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr FindWindowW(string lpClassName, string lpWindowName);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern uint RegisterWindowMessageW(string lpString);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool PostMessageW(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint processId);

    [DllImport("shell32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool Shell_NotifyIconW(uint dwMessage, ref NOTIFYICONDATA lpData);

    private static NOTIFYICONDATA Data(IntPtr hwnd)
    {
        var value = new NOTIFYICONDATA();
        value.cbSize = (uint)Marshal.SizeOf(typeof(NOTIFYICONDATA));
        value.hWnd = hwnd;
        value.uID = 1;
        return value;
    }

    public static IntPtr FindTrayWindow()
    {
        return FindWindowW(null, "CWapi Tray");
    }

    public static uint WindowProcessId(IntPtr hwnd)
    {
        uint pid;
        GetWindowThreadProcessId(hwnd, out pid);
        return pid;
    }

    public static bool RemoveIcon(IntPtr hwnd)
    {
        var data = Data(hwnd);
        return Shell_NotifyIconW(0x00000002, ref data);
    }

    public static uint TaskbarCreatedMessage()
    {
        return RegisterWindowMessageW("TaskbarCreated");
    }

    public static bool PostTaskbarCreated(IntPtr hwnd, uint message)
    {
        return PostMessageW(hwnd, message, IntPtr.Zero, IntPtr.Zero);
    }

    public static bool VerifyIconExists(IntPtr hwnd, string tooltip)
    {
        var data = Data(hwnd);
        data.uFlags = 0x00000004 | 0x00000080; // NIF_TIP | NIF_SHOWTIP
        data.szTip = tooltip;
        return Shell_NotifyIconW(0x00000001, ref data); // NIM_MODIFY
    }
}
"@

if ($ValidateOnly) {
    Write-Host 'PASS: PowerShell and native test helper compiled successfully.'
    return
}

$hwnd = [CWapiTrayTestNative]::FindTrayWindow()
if ($hwnd -eq [IntPtr]::Zero) {
    throw 'CWapi tray window was not found. Start CWapi first.'
}

$pidValue = [CWapiTrayTestNative]::WindowProcessId($hwnd)
if ($pidValue -eq 0) {
    throw 'Could not resolve the CWapi process from the tray window.'
}

$process = Get-Process -Id $pidValue -ErrorAction Stop
$version = $process.MainModule.FileVersionInfo.FileVersion
Write-Host "CWapi PID: $pidValue"
Write-Host "CWapi version: $version"
Write-Host 'Temporarily removing only the CWapi tray icon...'

if (-not [CWapiTrayTestNative]::RemoveIcon($hwnd)) {
    throw 'Could not remove the CWapi tray icon for the recovery test.'
}

Start-Sleep -Milliseconds 250
$message = [CWapiTrayTestNative]::TaskbarCreatedMessage()
if ($message -eq 0) {
    throw 'RegisterWindowMessage(TaskbarCreated) failed.'
}
if (-not [CWapiTrayTestNative]::PostTaskbarCreated($hwnd, $message)) {
    throw 'Could not send TaskbarCreated to the CWapi tray window.'
}

Start-Sleep -Milliseconds $WaitMilliseconds
$tooltip = if ($version) { "CWapi v$version" } else { 'CWapi' }
if (-not [CWapiTrayTestNative]::VerifyIconExists($hwnd, $tooltip)) {
    throw 'FAIL: CWapi did not restore its tray icon after TaskbarCreated. Restart CWapi to restore the icon.'
}

Write-Host 'PASS: CWapi restored its tray icon after a simulated TaskbarCreated event.'
Write-Host 'Explorer was not restarted and the CWapi process was not stopped.'
