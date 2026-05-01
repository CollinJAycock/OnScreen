; OnScreen — Windows MSI-style installer (Inno Setup 6.x)
;
; Produces a single self-contained .exe that bundles:
;   - server.exe + worker.exe + devtoken.exe (the product)
;   - ffmpeg.exe + ffprobe.exe (Gyan.dev full build)
;   - WinSW.exe (Windows Service wrapper)
;   - PostgreSQL 17 portable binaries
;   - Redis-for-Windows (tporadowski) — Memurai-equivalent, no eval timer
;
; Install flow:
;   1. Wizard prompts for install location (default C:\Program Files\OnScreen)
;   2. Files extracted to install dir
;   3. postinstall.ps1 runs:
;      - initdb new Postgres cluster in %ProgramData%\OnScreen\pgdata
;      - generates random Postgres password + SECRET_KEY
;      - creates `onscreen` database
;      - writes .env with DATABASE_URL / VALKEY_URL / SECRET_KEY
;      - registers + starts 3 Windows Services (Postgres, Redis, OnScreen)
;   4. Browser opens http://localhost:7070 for admin-account setup
;
; Uninstall flow:
;   1. preuninstall.ps1 stops + unregisters the 3 services (reverse dep order)
;   2. Inno Setup deletes program files
;   3. User is asked whether to delete the Postgres data dir + logs

#define MyAppName       "OnScreen Media Server"
#define MyAppShort      "OnScreen"
#define MyAppPublisher  "OnScreen"
#define MyAppURL        "https://github.com/onscreen/onscreen"
#define MyAppExeName    "server.exe"
#ifndef MyAppVersion
  #define MyAppVersion  "1.0.0"
#endif

[Setup]
AppId={{D6FCB7E2-2B5A-4E11-A9F7-OS-CR-INSTALLER}}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppShort}
DefaultGroupName={#MyAppShort}
DisableProgramGroupPage=yes
OutputBaseFilename=OnScreen-Setup-{#MyAppVersion}
OutputDir=..\..\dist
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
SetupLogging=yes
UninstallDisplayIcon={app}\{#MyAppExeName}
LicenseFile=..\..\LICENSE
DisableDirPage=auto
DisableReadyPage=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "openbrowser"; Description: "Open OnScreen in a browser when install completes"; GroupDescription: "Post-install:"

[Files]
; Core OnScreen binaries — produced by the build script before ISCC runs.
Source: "stage\server.exe";    DestDir: "{app}"; Flags: ignoreversion
Source: "stage\worker.exe";    DestDir: "{app}"; Flags: ignoreversion
Source: "stage\devtoken.exe";  DestDir: "{app}"; Flags: ignoreversion

; Service wrapper. Three running services share the same .exe via separate
; XML configs — that's how WinSW v2 works (the .exe stem matches the .xml
; stem at runtime). We deploy three copies so each service has its own
; <id>-named binary; renaming at install time is what binds them.
Source: "stage\WinSW.exe";     DestDir: "{app}"; Flags: ignoreversion

; Service XMLs — postinstall.ps1 substitutes {app}/{userpf} placeholders.
Source: "service-onscreen.xml"; DestDir: "{app}"; Flags: ignoreversion
Source: "service-postgres.xml"; DestDir: "{app}"; Flags: ignoreversion
Source: "service-redis.xml";    DestDir: "{app}"; Flags: ignoreversion

; Post/pre-install scripts.
Source: "postinstall.ps1";  DestDir: "{app}"; Flags: ignoreversion
Source: "preuninstall.ps1"; DestDir: "{app}"; Flags: ignoreversion

; Bundled deps. The build script extracts these into stage/ before ISCC
; compiles, so the installer carries them as plain files.
Source: "stage\ffmpeg\*"; DestDir: "{app}\ffmpeg"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "stage\pgsql\*";  DestDir: "{app}\pgsql";  Flags: ignoreversion recursesubdirs createallsubdirs
Source: "stage\redis\*";  DestDir: "{app}\redis";  Flags: ignoreversion recursesubdirs createallsubdirs

[Dirs]
; Pre-create %ProgramData%\OnScreen with permissive ACLs so the Postgres
; service account (LocalSystem by default) can write pgdata.
Name: "{commonappdata}\{#MyAppShort}";        Permissions: users-modify
Name: "{commonappdata}\{#MyAppShort}\pgdata"; Permissions: users-modify
Name: "{commonappdata}\{#MyAppShort}\logs";   Permissions: users-modify

[Icons]
Name: "{group}\Open {#MyAppShort}";  Filename: "http://localhost:7070"
Name: "{group}\OnScreen Logs";       Filename: "{commonappdata}\{#MyAppShort}\logs"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"

[Run]
; Run postinstall as admin in a hidden window. Errors propagate up
; (Inno will roll back the install on non-zero).
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postinstall.ps1"" -InstallDir ""{app}"""; \
  StatusMsg: "Initialising database and registering services..."; \
  Flags: runhidden waituntilterminated

; Open browser if the user opted in.
Filename: "http://localhost:7070"; \
  Description: "Open OnScreen now"; \
  Flags: shellexec postinstall nowait skipifsilent; \
  Tasks: openbrowser

[UninstallRun]
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\preuninstall.ps1"" -InstallDir ""{app}"""; \
  RunOnceId: "OnScreenSvcTeardown"; \
  Flags: runhidden waituntilterminated

[UninstallDelete]
; Clean up logs ffmpeg/Postgres might've written into the install dir.
Type: filesandordirs; Name: "{app}\logs"

[Code]
var
  RemoveDataPage: TInputOptionWizardPage;

procedure InitializeUninstallProgressForm();
begin
  // Inno Setup will show its own "Removing files..." progress; we just
  // need to make sure the pre-uninstall hook (UninstallRun) ran.
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  DataDir: string;
  Cleanup: Integer;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{commonappdata}\OnScreen');
    if DirExists(DataDir) then
    begin
      Cleanup := MsgBox(
        'Remove the OnScreen database and logs?'#13#10#13#10 +
        DataDir + #13#10#13#10 +
        'Choose "No" to keep your library / settings for a future reinstall.',
        mbConfirmation, MB_YESNO);
      if Cleanup = IDYES then
      begin
        DelTree(DataDir, True, True, True);
      end;
    end;
  end;
end;

procedure InitializeWizard();
begin
  // No extra wizard pages for now — keep the install simple.
end;
