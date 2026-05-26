; OnScreen — Windows MSI-style installer (Inno Setup 6.x)
;
; Produces a single self-contained .exe that bundles:
;   - server.exe + worker.exe + devtoken.exe (the product)
;   - ffmpeg.exe + ffprobe.exe (Gyan.dev full build)
;   - WinSW.exe (Windows Service wrapper)
;   - PostgreSQL 17 portable binaries
;   - Redis-for-Windows (tporadowski) — Memurai-equivalent, no eval timer
;
; Two install modes (selected on the wizard's mode page; postinstall.ps1 -Mode):
;
;   FULL (default, all-in-one box):
;     1. Wizard prompts for install location (default C:\Program Files\OnScreen)
;     2. Files extracted to install dir
;     3. postinstall.ps1 -Mode full runs:
;        - initdb new Postgres cluster in %ProgramData%\OnScreen\pgdata
;        - generates random Postgres password + SECRET_KEY
;        - creates `onscreen` database
;        - writes .env with DATABASE_URL / VALKEY_URL / SECRET_KEY
;        - registers + starts 3 Windows Services (Postgres, Redis, OnScreen)
;     4. Browser opens http://localhost:7070 for admin-account setup
;
;   WORKER (join an existing primary's fleet, no local DB):
;     1. Wizard takes the primary's DATABASE_URL / VALKEY_URL / SECRET_KEY +
;        this node's WORKER_ADDR
;     2. postinstall.ps1 -Mode worker writes .env and opens the segment port
;     3. The worker is registered as an onlogon INTERACTIVE scheduled task
;        (OnScreenWorker) — NOT a Windows service. A service runs in session 0
;        with no GPU access, which makes NVENC/QSV probing fail at startup
;        (the historical Event 7023 on the OnScreenWorker service). The task
;        runs in the install user's logged-on session so the worker can reach
;        the GPU, and auto-restarts on failure
;
; Uninstall flow:
;   1. preuninstall.ps1 stops + unregisters the 3 Windows services and the
;      OnScreenWorker scheduled task (reverse-dependency order)
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
; Worker-only mode uses this instead of the three above.
Source: "service-worker.xml";   DestDir: "{app}"; Flags: ignoreversion

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
; Full server: initialise DB + register Postgres/Redis/OnScreen services.
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postinstall.ps1"" -InstallDir ""{app}"""; \
  StatusMsg: "Initialising database and registering services..."; \
  Flags: runhidden waituntilterminated; \
  Check: IsFullMode

; Worker only: write a .env pointing at the primary + register the worker
; service. The wizard wrote the connection details to {tmp}\onscreen-worker.conf.
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\postinstall.ps1"" -InstallDir ""{app}"" -Mode worker -WorkerConfig ""{tmp}\onscreen-worker.conf"""; \
  StatusMsg: "Registering transcode worker..."; \
  Flags: runhidden waituntilterminated; \
  Check: IsWorkerMode

; Open browser if the user opted in (full server only — a worker has no UI).
Filename: "http://localhost:7070"; \
  Description: "Open OnScreen now"; \
  Flags: shellexec postinstall nowait skipifsilent; \
  Tasks: openbrowser; \
  Check: IsFullMode

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
  SetupTypePage: TInputOptionWizardPage;
  WorkerPage: TInputQueryWizardPage;

// Worker-only is option index 1 on the setup-type page.
function IsWorkerMode(): Boolean;
begin
  Result := (SetupTypePage <> nil) and (SetupTypePage.SelectedValueIndex = 1);
end;

function IsFullMode(): Boolean;
begin
  Result := not IsWorkerMode();
end;

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
  // Setup-type chooser: full all-in-one server vs. a transcode worker that
  // joins an existing server's fleet.
  SetupTypePage := CreateInputOptionPage(wpSelectDir,
    'Setup Type', 'How should this machine run OnScreen?',
    'Pick an install type, then click Next.',
    True {exclusive radio}, False);
  SetupTypePage.Add('Full server  —  bundled database + media server (default; one machine)');
  SetupTypePage.Add('Worker only  —  transcode worker that joins an existing OnScreen server''s fleet');
  SetupTypePage.SelectedValueIndex := 0;

  // Worker connection details. Shown only when "Worker only" is selected.
  WorkerPage := CreateInputQueryPage(SetupTypePage.ID,
    'Worker Configuration', 'Connect this worker to the primary OnScreen server',
    'Enter the primary server''s shared connection details. The primary''s Postgres and Valkey must be reachable from this machine over the network, and SECRET_KEY must match the primary''s exactly.');
  WorkerPage.Add('Primary DATABASE_URL  (postgres://postgres:PASS@HOST:5432/onscreen?sslmode=disable):', False);
  WorkerPage.Add('Primary VALKEY_URL  (redis://HOST:6379):', False);
  WorkerPage.Add('SECRET_KEY  (copy from the primary''s .env — must match):', False);
  WorkerPage.Add('This machine''s WORKER_ADDR  (host:port other nodes reach it on, e.g. 10.0.0.5:7073):', False);
  WorkerPage.Values[3] := ':7073';
end;

// Skip the worker-config page for a full-server install.
function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := False;
  if (WorkerPage <> nil) and (PageID = WorkerPage.ID) then
    Result := IsFullMode();
end;

// Validate worker fields and stash them in a temp file the [Run] postinstall
// reads (keeps the SECRET_KEY / DSN off the command line).
function NextButtonClick(CurPageID: Integer): Boolean;
var
  conf: string;
begin
  Result := True;
  if (WorkerPage <> nil) and (CurPageID = WorkerPage.ID) then
  begin
    if (Trim(WorkerPage.Values[0]) = '') or (Trim(WorkerPage.Values[1]) = '') or
       (Trim(WorkerPage.Values[2]) = '') or (Trim(WorkerPage.Values[3]) = '') then
    begin
      MsgBox('All four fields are required for a worker install.', mbError, MB_OK);
      Result := False;
      Exit;
    end;
    conf :=
      'DATABASE_URL=' + Trim(WorkerPage.Values[0]) + #13#10 +
      'VALKEY_URL='   + Trim(WorkerPage.Values[1]) + #13#10 +
      'SECRET_KEY='   + Trim(WorkerPage.Values[2]) + #13#10 +
      'WORKER_ADDR='  + Trim(WorkerPage.Values[3]) + #13#10;
    if not SaveStringToFile(ExpandConstant('{tmp}\onscreen-worker.conf'), conf, False) then
    begin
      MsgBox('Could not write the temporary worker config file.', mbError, MB_OK);
      Result := False;
    end;
  end;
end;
