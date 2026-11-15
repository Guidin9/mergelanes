; Inno Setup script. Built by scripts/build.ps1 (needs dist/ filled first):
;   ISCC /DVersion=0.1.0 installer\mergelanes.iss
#ifndef Version
  #define Version "0.0.0"
#endif

[Setup]
AppId={{5C9E7B32-1D4A-4F86-B0E3-7A2D9C6F18B4}
AppName=MergeLanes
AppVersion={#Version}
AppVerName=MergeLanes {#Version}
AppPublisher=Mert Demirdöven
#ifdef RepoUrl
AppPublisherURL={#RepoUrl}
AppSupportURL={#RepoUrl}/issues
AppUpdatesURL={#RepoUrl}/releases
#endif
VersionInfoVersion={#Version}
DefaultDirName={autopf}\MergeLanes
DefaultGroupName=MergeLanes
DisableProgramGroupPage=yes
; The TUN adapter and routes need admin, and so does writing to Program Files.
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=LICENSE
SourceDir=..
OutputDir=dist
OutputBaseFilename=mergelanes-setup-{#Version}
UninstallDisplayIcon={app}\mergelanes.ico
CloseApplications=no
; Uninstall removes %ProgramData%\MergeLanes (status, log, limits and app rules).
UsedUserAreasWarning=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "autostart"; Description: "Start MergeLanes when I sign in (no UAC prompt)"; GroupDescription: "Startup:"; Flags: unchecked
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Shortcuts:"; Flags: unchecked

[Files]
Source: "dist\mergelanes.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\mergelanes-tray.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\wintun-LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\mergelanes.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "LICENSE-net-merge"; DestDir: "{app}"; Flags: ignoreversion
Source: "THIRD-PARTY-NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion isreadme

[Icons]
Name: "{autoprograms}\MergeLanes"; Filename: "{app}\mergelanes-tray.exe"; IconFilename: "{app}\mergelanes.ico"; Comment: "Use all your network adapters at once, with a lane for each app"
Name: "{autodesktop}\MergeLanes"; Filename: "{app}\mergelanes-tray.exe"; IconFilename: "{app}\mergelanes.ico"; Tasks: desktopicon

[Run]
Filename: "{app}\mergelanes.exe"; Parameters: "autostart on"; Flags: runhidden; Tasks: autostart; StatusMsg: "Enabling start at sign-in..."
; The installer is already elevated, so the tray starts without a second UAC prompt.
Filename: "{app}\mergelanes-tray.exe"; Description: "Start MergeLanes now"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\mergelanes.exe"; Parameters: "autostart off"; Flags: runhidden; RunOnceId: "AutostartOff"

[UninstallDelete]
Type: filesandordirs; Name: "{commonappdata}\MergeLanes"

[Code]
// Turn MergeLanes off and make sure no copy is running before files are replaced or removed.
// Killing it is safe: the TUN adapter and its routes disappear with the process.
procedure StopMergeLanes;
var
  rc: Integer;
begin
  if FileExists(ExpandConstant('{app}\mergelanes.exe')) then
    Exec(ExpandConstant('{app}\mergelanes.exe'), 'quit', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM mergelanes-tray.exe', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM mergelanes.exe', '', SW_HIDE, ewWaitUntilTerminated, rc);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopMergeLanes;
  Result := '';
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    StopMergeLanes;
end;
