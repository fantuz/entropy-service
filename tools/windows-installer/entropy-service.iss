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
DefaultDirName={autopf}\Entropy Service
DefaultGroupName=Entropy Service
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0
OutputDir={#OutputDir}
OutputBaseFilename=entropy-service_{#AppVersion}_windows_amd64_gui_setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
DisableWelcomePage=no
DisableProgramGroupPage=yes
UsePreviousAppDir=yes
UsePreviousSetupType=yes
DisableDirPage=auto
CloseApplications=yes
RestartApplications=no
UninstallDisplayIcon={app}\runtime\bin\pythonw.exe
LicenseFile={#BundleDir}\LICENSE

[Types]
Name: "full"; Description: "Full installation"
Name: "english"; Description: "English web interface only"
Name: "custom"; Description: "Custom installation"; Flags: iscustom

[Components]
Name: "core"; Description: "Core application and all random-word dictionaries"; Types: full english custom; Flags: fixed
Name: "web_locales"; Description: "Additional web interface languages"; Types: full
Name: "web_locales\fr"; Description: "French"; Types: full
Name: "web_locales\de"; Description: "German"; Types: full
Name: "web_locales\es"; Description: "Spanish"; Types: full
Name: "web_locales\cs"; Description: "Czech"; Types: full
Name: "web_locales\ru"; Description: "Russian"; Types: full
Name: "web_locales\pl"; Description: "Polish"; Types: full
Name: "web_locales\el"; Description: "Greek"; Types: full
Name: "web_locales\it"; Description: "Italian"; Types: full
Name: "web_locales\nl"; Description: "Dutch"; Types: full
Name: "web_locales\pt_pt"; Description: "Portuguese (Portugal)"; Types: full
Name: "web_locales\pt_br"; Description: "Portuguese (Brazil)"; Types: full
Name: "web_locales\ja"; Description: "Japanese"; Types: full
Name: "web_locales\ar"; Description: "Arabic"; Types: full

[Files]
Source: "{#BundleDir}\*"; DestDir: "{app}"; Excludes: "bin\ui-locales\*"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#BundleDir}\bin\ui-locales\en.json"; DestDir: "{app}\bin\ui-locales"; Components: core; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\fr.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\fr; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\de.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\de; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\es.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\es; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\cs.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\cs; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\ru.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\ru; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\pl.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\pl; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\el.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\el; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\it.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\it; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\nl.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\nl; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\pt-PT.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\pt_pt; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\pt-BR.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\pt_br; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\ja.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\ja; Flags: ignoreversion
Source: "{#BundleDir}\bin\ui-locales\ar.json"; DestDir: "{app}\bin\ui-locales"; Components: web_locales\ar; Flags: ignoreversion

[Tasks]
Name: "server_service"; Description: "Install entropy-server as a Windows Service"; GroupDescription: "Optional machine-wide service:"; Flags: unchecked checkablealone; Check: IsAdminInstallMode
Name: "server_service\start"; Description: "Start the server service after installation"; Flags: unchecked dontinheritcheck; Check: IsAdminInstallMode

[Icons]
Name: "{group}\Entropy Server"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view server"; WorkingDir: "{app}"
Name: "{group}\Entropy Client"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view client"; WorkingDir: "{app}"
Name: "{group}\GUI guide"; Filename: "{app}\docs\windows-gui-installer.md"
Name: "{group}\Windows Services"; Filename: "{sys}\mmc.exe"; Parameters: "services.msc"; Check: WindowsServiceMenu

[Run]
Filename: "{group}"; Description: "Open the Entropy Service Start Menu folder"; Flags: postinstall shellexec nowait skipifsilent

[Code]
const
  UninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{C5571481-03FA-4233-92FC-B0EF9BA76A44}_is1';
  ProjectCredit = 'Software Architecture, Build Pipelines and Security Audit by Massimiliano Fantuzzi';
var
  PreviousDirectory: String;
  ReplacementPage: TOutputMsgWizardPage;
  UpgradeProgress: TOutputMarqueeProgressWizardPage;
  RemovedEntries: Integer;
  ServiceInfoPage: TOutputMsgMemoWizardPage;
  ServiceWasManaged, ServiceWasRunning, ServiceStopped, UpgradeCleanupStarted: Boolean;

function SetOwnedAttributes(const Filename: String; Attributes: LongWord): Boolean;
  external 'SetFileAttributesW@kernel32.dll stdcall';

function ReplacingInstallation: Boolean;
begin
  Result := PreviousDirectory <> '';
end;

#include "service.iss"

procedure InitializeWizard;
var
  PreviousVersion, LicenseText: String;
begin
  { Keep this within the standard welcome label's measured bounds. Appending
    paragraphs to Inno's fixed-height default label can silently hide its end. }
  WizardForm.WelcomeLabel2.Caption := 'Install Entropy Service {#AppVersion} and its desktop applications.' + #13#10#13#10 +
    ProjectCredit + #13#10#13#10 + 'Released under the MIT License, shown on the next page.' + #13#10#13#10 +
    'Current-user setup needs no administrator rights. Choose all-users setup to enable the optional Windows Service.';
  WizardForm.SelectComponentsLabel.Caption :=
    'Choose additional web interface languages. English and all random-word dictionaries are always installed.';
  { Reflow display paragraphs rather than retaining the plain-text file's
    printing-width line breaks. The installed LICENSE is kept verbatim. }
  LicenseText := WizardForm.LicenseMemo.Lines.Text;
  StringChangeEx(LicenseText, #13#10#13#10, #1, True);
  StringChangeEx(LicenseText, #13#10, ' ', True);
  StringChangeEx(LicenseText, #1, #13#10#13#10, True);
  StringChangeEx(LicenseText, 'THE SOFTWARE', 'THE' + #160 + 'SOFTWARE', True);
  WizardForm.LicenseMemo.WordWrap := True;
  WizardForm.LicenseMemo.Lines.Text := ProjectCredit + #13#10#13#10 + Trim(LicenseText);
  UpgradeProgress := CreateOutputMarqueeProgressPage('Upgrading Entropy Service',
    'Preparing the existing installation for replacement.');
  if not RegQueryStringValue(InstallRegistrationRoot, UninstallKey, 'Inno Setup: App Path', PreviousDirectory) then
    PreviousDirectory := '';
  if not RegQueryStringValue(InstallRegistrationRoot, UninstallKey, 'DisplayVersion', PreviousVersion) then
    PreviousVersion := 'Unknown';
  ReplacementPage := CreateOutputMsgPage(wpLicense, 'Existing installation found',
    'Review the replacement before continuing.',
    'Installed version: ' + PreviousVersion + #13#10 +
    'Setup version: {#AppVersion}' + #13#10 +
    'Location: ' + PreviousDirectory + #13#10#13#10 +
    'Setup will replace the installed application and its bundled runtime. No separate uninstall is needed.' + #13#10#13#10 +
    'Close Entropy Service windows before continuing. Custom files inside the program and runtime folders will be removed. ' +
    'User data, certificates and exports outside those folders are kept. Cancel now to leave the current installation unchanged.');
  InitializeServiceOptions;
end;

procedure CleanupOwnedEntry(const Filename: String);
var
  Entry, Child: TFindRec;
  Attributes: LongWord;
  Removed: Boolean;
begin
  if not FindFirst(Filename, Entry) then
  begin
    if FileExists(Filename) or DirExists(Filename) then
      RaiseException('Cannot inspect the previous installation: ' + Filename);
    Exit;
  end;
  Attributes := Entry.Attributes;
  FindClose(Entry);
  { Never follow directory junctions/symlinks outside the installer-owned
    trees. Remove only the link, just as Inno's built-in DelTree does. }
  if ((Attributes and FILE_ATTRIBUTE_DIRECTORY) <> 0) and
     ((Attributes and FILE_ATTRIBUTE_REPARSE_POINT) = 0) then
    if FindFirst(AddBackslash(Filename) + '*', Child) then
    begin
      try
        repeat
          if (Child.Name <> '.') and (Child.Name <> '..') then
            CleanupOwnedEntry(AddBackslash(Filename) + Child.Name);
        until not FindNext(Child);
      finally
        FindClose(Child);
      end;
    end;
  if ((Attributes and FILE_ATTRIBUTE_READONLY) <> 0) and
     ((Attributes and FILE_ATTRIBUTE_REPARSE_POINT) = 0) then
    if not SetOwnedAttributes(Filename, Attributes and not FILE_ATTRIBUTE_READONLY) then
      RaiseException('Cannot prepare an old application file for replacement: ' + Filename);
  if (Attributes and FILE_ATTRIBUTE_DIRECTORY) <> 0 then
    Removed := RemoveDir(Filename)
  else
    Removed := DeleteFile(Filename);
  if not Removed then
    RaiseException('Cannot remove an old application file: ' + Filename + #13#10 +
      'Close applications using this installation, check file permissions, then run setup again.');
  RemovedEntries := RemovedEntries + 1;
  if RemovedEntries mod 32 = 0 then
    UpgradeProgress.SetText('Removing previous application and runtime files...',
      IntToStr(RemovedEntries) + ' files and folders removed');
  UpgradeProgress.Animate;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssInstall) and ReplacingInstallation then
  begin
    UpgradeCleanupStarted := True;
    RemovedEntries := 0;
    UpgradeProgress.SetText('Removing previous application and runtime files...',
      'The private Python/GTK runtime contains many small files. Please keep setup open.');
    UpgradeProgress.Show;
    try
      Log('Upgrade cleanup started');
      CleanupOwnedEntry(ExpandConstant('{app}\app'));
      CleanupOwnedEntry(ExpandConstant('{app}\bin'));
      CleanupOwnedEntry(ExpandConstant('{app}\runtime'));
      CleanupOwnedEntry(ExpandConstant('{app}\runtime-package-metadata'));
      CleanupOwnedEntry(ExpandConstant('{app}\docs'));
      Log('Upgrade cleanup completed: ' + IntToStr(RemovedEntries) + ' files and folders removed');
    finally
      UpgradeProgress.Hide;
    end;
    WizardForm.StatusLabel.Caption := 'Installing the replacement application and runtime...';
  end
  else if CurStep = ssPostInstall then
    InstallOrResumeService
  else if (CurStep = ssDone) and IsAdminInstallMode and ServiceWasManaged then
    RegWriteDWordValue(HKLM64, UninstallKey, 'EntropyServiceManaged', 1);
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (PageID = ReplacementPage.ID) and not ReplacingInstallation;
  if PageID = ServiceInfoPage.ID then
    Result := not (WindowsServiceSelected or ServiceWasManaged);
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
      UpgradeProgress.Animate;
      RuntimeProcess := Processes.ItemIndex(Index);
      if PrivateProcessKind(RuntimeProcess) = 1 then
      begin
        Result := 'Close the Entropy Service GUI windows and local utilities using this installation, then retry setup.';
        Exit;
      end;
    end;
    for Index := 0 to Processes.Count - 1 do
    begin
      UpgradeProgress.Animate;
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
  if Result = '' then Result := PrepareManagedService;
  if (Result = '') and ReplacingInstallation then
  begin
    UpgradeProgress.SetText('Checking running applications and private runtime helpers...',
      'No files have been removed. Close Entropy Service windows if setup asks.');
    UpgradeProgress.Show;
    try
      Result := ClosePrivateRuntimeHelpers;
    finally
      UpgradeProgress.Hide;
    end;
  end;
  if Result <> '' then RestoreStoppedService;
end;
