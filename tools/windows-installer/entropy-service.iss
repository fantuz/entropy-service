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

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  if ReplacingInstallation and
     (CompareText(AddBackslash(ExpandFileName(WizardDirValue)), AddBackslash(ExpandFileName(PreviousDirectory))) <> 0) then
    Result := 'An Entropy Service installation already exists at ' + PreviousDirectory + '.' + #13#10 +
      'Upgrade in that location, or uninstall it through Windows Apps before choosing a different folder.';
end;
