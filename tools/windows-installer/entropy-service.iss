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
UninstallDisplayIcon={app}\runtime\bin\pythonw.exe
LicenseFile={#BundleDir}\LICENSE

[Files]
Source: "{#BundleDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Entropy Server"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view server"; WorkingDir: "{app}"
Name: "{group}\Entropy Client"; Filename: "{app}\runtime\bin\pythonw.exe"; Parameters: "-I -B ""{app}\app\gui_bootstrap.py"" --view client"; WorkingDir: "{app}"
Name: "{group}\GUI guide"; Filename: "{app}\docs\windows-gui-installer.md"
