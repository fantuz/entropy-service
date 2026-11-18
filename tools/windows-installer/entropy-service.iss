#ifndef BundleDir
  #error BundleDir is required
#endif
#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef OutputDir
  #error OutputDir is required
#endif

[Setup]
AppId={{C5571481-03FA-4233-92FC-B0EF9BA76A44}
AppName=Entropy Service
AppVersion={#AppVersion}
VersionInfoVersion={#NumericVersion}
AppPublisher=Entropy Service contributors
AppPublisherURL=https://github.com/fantuz/entropy-service
AppSupportURL=https://github.com/fantuz/entropy-service/issues
DefaultDirName={localappdata}\Programs\Entropy Service
DefaultGroupName=Entropy Service
PrivilegesRequired=lowest
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0
OutputDir={#OutputDir}
OutputBaseFilename=entropy-service_{#AppVersion}_windows_amd64_gui_setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
DisableProgramGroupPage=yes
UsePreviousAppDir=yes
DisableDirPage=auto
CloseApplications=yes
RestartApplications=no
UninstallDisplayIcon={app}\runtime\bin\pythonw.exe
LicenseFile={#BundleDir}\LICENSE

[Files]
Source: "{#BundleDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[InstallDelete]
; These are installer-owned program trees, never the user's separate data directory.
Type: filesandordirs; Name: "{app}\app"; Check: ReplacingInstallation
Type: filesandordirs; Name: "{app}\bin"; Check: ReplacingInstallation
Type: filesandordirs; Name: "{app}\runtime"; Check: ReplacingInstallation
Type: filesandordirs; Name: "{app}\runtime-package-metadata"; Check: ReplacingInstallation
Type: filesandordirs; Name: "{app}\docs"; Check: ReplacingInstallation

[Icons]
Name: "{group}\Entropy Server"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view server"; WorkingDir: "{app}"
Name: "{group}\Entropy Client"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view client"; WorkingDir: "{app}"
Name: "{group}\GUI guide"; Filename: "{app}\docs\windows-gui-installer.md"

[Run]
Filename: "{group}"; Description: "Open the Entropy Service Start Menu folder"; Flags: postinstall shellexec nowait skipifsilent

[Code]
const
  UninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{C5571481-03FA-4233-92FC-B0EF9BA76A44}_is1';
var
  PreviousDirectory: String;
  ReplacementPage: TOutputMsgWizardPage;

function ReplacingInstallation: Boolean;
begin
  Result := PreviousDirectory <> '';
end;

procedure InitializeWizard;
var
  PreviousVersion: String;
begin
  if not RegQueryStringValue(HKCU64, UninstallKey, 'Inno Setup: App Path', PreviousDirectory) then
    PreviousDirectory := '';
  if not RegQueryStringValue(HKCU64, UninstallKey, 'DisplayVersion', PreviousVersion) then
    PreviousVersion := 'Unknown';
  ReplacementPage := CreateOutputMsgPage(wpLicense, 'Existing installation found',
    'Review the replacement before continuing.',
    'Installed version: ' + PreviousVersion + #13#10 +
    'Setup version: {#AppVersion}' + #13#10 +
    'Location: ' + PreviousDirectory + #13#10#13#10 +
    'Setup will replace the installed application and its bundled runtime. No separate uninstall is needed.' + #13#10#13#10 +
    'Close Entropy Service windows before continuing. Custom files inside the program and runtime folders will be removed. ' +
    'User data, certificates and exports outside those folders are kept. Cancel now to leave the current installation unchanged.');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (PageID = ReplacementPage.ID) and not ReplacingInstallation;
end;

function PrivateProcessKind(RuntimeProcess: Variant): Integer;
var
  Filename, RuntimeRoot, BinaryRoot, ProcessName: String;
begin
  Result := 0;
  try
    if VarIsNull(RuntimeProcess.ExecutablePath) then
      Exit;
    Filename := RuntimeProcess.ExecutablePath;
    ProcessName := RuntimeProcess.Name;
  except
    { A process can exit between the WMI snapshot and reading its properties.
      Skip that stale entry rather than abandoning the remaining live entries. }
    Exit;
  end;
  if Filename = '' then
    Exit;
  { Normalize existing long/8.3 paths before comparing private roots. }
  Filename := GetShortName(Filename);
  RuntimeRoot := AddBackslash(GetShortName(PreviousDirectory + '\runtime'));
  BinaryRoot := AddBackslash(GetShortName(PreviousDirectory + '\bin'));
  if (CompareText(Copy(Filename, 1, Length(RuntimeRoot)), RuntimeRoot) <> 0) and
     (CompareText(Copy(Filename, 1, Length(BinaryRoot)), BinaryRoot) <> 0) then
    Exit;
  ProcessName := Lowercase(ProcessName);
  if (ProcessName = 'gdbus.exe') or (ProcessName = 'dbus-daemon.exe') then
    Result := 2
  else
    Result := 1;
end;

function ClosePrivateRuntimeHelpers: String;
var
  Locator, Services, Processes, RuntimeProcess: Variant;
  Index, Status: Integer;
  HelperPath: String;
begin
  Result := '';
  try
    Locator := CreateOleObject('WbemScripting.SWbemLocator');
    Services := Locator.ConnectServer('', 'root\CIMV2');
    Processes := Services.ExecQuery('SELECT * FROM Win32_Process');
    { Check every live application first: never kill a GUI, CLI or unrelated
      process merely because it maps one of the runtime DLLs. }
    for Index := 0 to Processes.Count - 1 do
    begin
      RuntimeProcess := Processes.ItemIndex(Index);
      if PrivateProcessKind(RuntimeProcess) = 1 then
      begin
        Result := 'Close the Entropy Service GUI windows and local utilities using this installation, then retry setup.';
        Exit;
      end;
    end;
    for Index := 0 to Processes.Count - 1 do
    begin
      RuntimeProcess := Processes.ItemIndex(Index);
      if PrivateProcessKind(RuntimeProcess) = 2 then
      begin
        HelperPath := RuntimeProcess.ExecutablePath;
        Status := RuntimeProcess.Terminate(0);
        if (Status <> 0) and (Status <> 9) then
        begin
          Result := 'A private GTK background helper could not be stopped. Close it and retry setup before replacing the runtime.';
          Exit;
        end;
        Log('Closed private GTK runtime helper: ' + HelperPath);
        Sleep(100);
      end;
    end;
  except
    { Windows' normal Restart Manager remains the fallback when WMI access is
      disabled by policy. Do not weaken file-in-use checks or force-close apps. }
    Log('Private GTK helper inspection unavailable: ' + GetExceptionMessage);
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  if ReplacingInstallation and
     (CompareText(AddBackslash(ExpandFileName(WizardDirValue)), AddBackslash(ExpandFileName(PreviousDirectory))) <> 0) then
    Result := 'An Entropy Service installation already exists at ' + PreviousDirectory + '.' + #13#10 +
      'Upgrade in that location, or uninstall it through Windows Apps before choosing a different folder.';
  if (Result = '') and ReplacingInstallation then
    Result := ClosePrivateRuntimeHelpers;
end;
