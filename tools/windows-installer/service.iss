; Optional machine-wide SCM integration. Included inside the recipe's [Code].
function InstallRegistrationRoot: Integer;
begin
  if IsAdminInstallMode then Result := HKLM64 else Result := HKCU64;
end;

function OtherRegistrationRoot: Integer;
begin
  if IsAdminInstallMode then Result := HKCU64 else Result := HKLM64;
end;

function WindowsServiceSelected: Boolean;
begin
  Result := IsAdminInstallMode and WizardIsTaskSelected('server_service');
end;

function WindowsServiceMenu: Boolean;
begin
  Result := IsAdminInstallMode and (WindowsServiceSelected or ServiceWasManaged);
end;

function ServiceRegistrationExists: Boolean;
begin
  Result := RegKeyExists(HKLM64, 'SYSTEM\CurrentControlSet\Services\EntropyServer');
end;

function InstallerOwnsService: Boolean;
var
  Owned: Cardinal;
begin
  Result := IsAdminInstallMode and
    RegQueryDWordValue(HKLM64, UninstallKey, 'EntropyServiceManaged', Owned) and (Owned = 1);
end;

function RunManagedService(const Operation: String; var State: String): Boolean;
var
  Code, Index: Integer;
  Output: TExecOutput;
begin
  State := '';
  Code := -1;
  try
    Result := ExecAndCaptureOutput(ExpandConstant('{app}\bin\entropy-server.exe'),
      'service ' + Operation, ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, Code, Output);
    Result := Result and (Code = 0) and not Output.Error;
    Log('Windows Service ' + Operation + ': exit ' + IntToStr(Code));
    { Status includes the full service command. Inspect only the state line;
      do not copy operator arguments or credentials into the installer log. }
    for Index := 0 to GetArrayLength(Output.StdOut) - 1 do
      if Pos('State: ', Output.StdOut[Index]) = 1 then
        State := Copy(Output.StdOut[Index], 8, Length(Output.StdOut[Index]));
  except
    Log('Windows Service command could not run: ' + GetExceptionMessage);
    Result := False;
  end;
end;

function ServiceLocationSafe: Boolean;
var
  Directory, Parent, Root: String;
  Entry: TFindRec;
begin
  Directory := ExpandFileName(WizardDirValue);
  Root := AddBackslash(ExpandFileName(ExpandConstant('{pf64}')));
  Result := (Length(AddBackslash(Directory)) > Length(Root)) and
    (CompareText(Copy(AddBackslash(Directory), 1, Length(Root)), Root) = 0);
  if not Result then Exit;
  { Program Files supplies the administrative parent. Reject directory links
    rather than treating a redirected user-writable tree as a protected path. }
  repeat
    if FindFirst(Directory, Entry) then
    begin
      Result := (Entry.Attributes and FILE_ATTRIBUTE_REPARSE_POINT) = 0;
      FindClose(Entry);
      if not Result then Exit;
    end;
    Parent := ExtractFileDir(Directory);
    if Parent = Directory then Break;
    Directory := Parent;
  until Directory = '';
  { A fresh service install must not adopt an arbitrary pre-existing tree. }
  if not ReplacingInstallation and DirExists(WizardDirValue) then Result := False;
end;

procedure InitializeServiceOptions;
begin
  ServiceWasManaged := InstallerOwnsService and ServiceRegistrationExists;
  ServiceInfoPage := CreateOutputMsgMemoPage(wpSelectTasks, 'Optional Windows server service',
    'Review the persistent service before installing.',
    'Service configuration and operating notes:',
    'Service: EntropyServer, automatic delayed startup, account NT SERVICE\EntropyServer.' + #13#10#13#10 +
    'HTTP: http://127.0.0.1:8080/  HTTPS: https://127.0.0.1:8443/' + #13#10 +
    'A local self-signed certificate is created on first start and lasts seven days. Renewal is manual. No firewall or trust-store changes are made.' + #13#10#13#10 +
    'State, certificates and logs: ' + ExpandConstant('{commonappdata}\entropy-service') + '. They are retained when uninstalling.' + #13#10#13#10 +
    'Use Windows Services or entropy-server service commands for Start/Stop. The GUI can Connect to monitor it; GUI startup options do not reconfigure SCM.' + #13#10#13#10 +
    'An existing installer-managed service is upgraded in place and resumed only if it was running or Start now is selected. Leaving Install unchecked does not remove an existing service.');
end;

function PrepareManagedService: String;
var
  OtherDirectory, State: String;
begin
  Result := '';
  if RegQueryStringValue(OtherRegistrationRoot, UninstallKey, 'Inno Setup: App Path', OtherDirectory) then
  begin
    Result := 'Entropy Service is already installed in the other installation mode at ' + OtherDirectory + '.' + #13#10 +
      'Choose its existing current-user/all-users mode to upgrade, or uninstall that copy before switching modes. Separate user data is retained.';
    Exit;
  end;
  if WindowsServiceSelected and not ServiceLocationSafe then
  begin
    Result := 'A Windows Service requires a protected installation under Program Files without directory links. Use the default new folder or the existing machine-wide installation.';
    Exit;
  end;
  if WindowsServiceSelected and ServiceRegistrationExists and not ServiceWasManaged then
  begin
    Result := 'EntropyServer is already registered outside this installer. Setup will not replace or control it. Leave the service option unchecked and manage that registration explicitly.';
    Exit;
  end;
  if ServiceWasManaged then
  begin
    UpgradeProgress.SetText('Preparing the installed Windows Service...', 'Stopping it gracefully before replacing application files.');
    UpgradeProgress.Show;
    try
      if not RunManagedService('status', State) then
        Result := 'The existing service no longer matches this installation. Check its registration before upgrading.'
      else
      begin
        ServiceWasRunning := (State = 'running') or (State = 'starting');
        if RunManagedService('stop', State) then ServiceStopped := True
        else Result := 'The Windows Service could not stop gracefully. Check its logs and stop it before retrying setup.';
      end;
    finally
      UpgradeProgress.Hide;
    end;
  end;
end;

procedure InstallOrResumeService;
var
  State: String;
begin
  if not (WindowsServiceSelected or ServiceWasManaged) then Exit;
  UpgradeProgress.SetText('Configuring the optional Windows Service...', 'User state, certificates and existing service options are retained.');
  UpgradeProgress.Show;
  try
    if not ServiceWasManaged then
    begin
      if not RunManagedService('install -self-signed', State) then
        RaiseException('The application was installed, but Windows Service registration failed. Check the protected state directory and the Windows Service guide.');
      ServiceWasManaged := True;
    end;
    if not RegWriteDWordValue(HKLM64, UninstallKey, 'EntropyServiceManaged', 1) then
      RaiseException('Could not record Windows Service ownership for safe upgrades and uninstall.');
    if ServiceWasRunning or WizardIsTaskSelected('server_service\start') then
      if not RunManagedService('start', State) then
        RaiseException('The service is installed but could not start. Check ports 8080/8443, certificates and logs under ProgramData\entropy-service.');
    ServiceStopped := False;
  finally
    UpgradeProgress.Hide;
  end;
end;

procedure RestoreStoppedService;
var
  State: String;
begin
  if ServiceStopped and ServiceWasRunning and not UpgradeCleanupStarted then
  begin
    if not RunManagedService('start', State) then
      Log('The original service could not resume after setup was cancelled; start it explicitly.');
    ServiceStopped := False;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  State: String;
begin
  if (CurUninstallStep = usUninstall) and InstallerOwnsService and ServiceRegistrationExists then
  begin
    if not RunManagedService('stop', State) or not RunManagedService('uninstall', State) then
      RaiseException('The installer-managed service could not be removed safely. No service state or certificates were deleted. Check its registration and stop it before retrying.');
    Log('Removed installer-managed Windows Service; state, certificates and logs retained.');
  end;
end;

procedure DeinitializeSetup;
begin
  RestoreStoppedService;
end;
