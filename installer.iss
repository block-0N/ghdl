; ghfast Windows 安装包脚本
; 用 Inno Setup 6 编译

#define MyAppName "ghfast"
#define MyAppPublisher "block-0N"
#define MyAppURL "https://block-0n.github.io/ghfast/"
#define MyAppExeName "ghfast.exe"

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif

[Setup]
AppId={{9A3F7B21-4D8E-4C6A-9F1B-8E2D5C7A4B93}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=admin
OutputDir=dist
OutputBaseFilename=ghfast-setup
SetupIconFile=ghfast.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayIcon={app}\{#MyAppExeName}

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "addtopath"; Description: "将 ghfast 加入系统 PATH 环境变量（推荐）"; GroupDescription: "安装选项："; Flags: checkedonce
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "安装选项："; Flags: unchecked

[Files]
Source: "ghfast-windows-amd64.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "查看 ghfast 用法"; Flags: postinstall nowait skipifsilent

[Code]
const
    EnvironmentKey = 'Environment';
    WM_SETTINGCHANGE = $001A;
    SMTO_ABORTIFHUNG = $0002;

procedure EnvAddPath(Path: string);
var
    Paths: string;
begin
    if not RegQueryStringValue(HKEY_LOCAL_MACHINE, EnvironmentKey, 'Path', Paths)
    then Paths := '';

    if Pos(';' + Uppercase(Path) + ';', ';' + Uppercase(Paths) + ';') > 0 then exit;

    if Paths = '' then
        Paths := Path
    else
        Paths := Paths + ';' + Path;

    if RegWriteStringValue(HKEY_LOCAL_MACHINE, EnvironmentKey, 'Path', Paths)
    then Log(Format('已添加 [%s] 到系统 PATH', [Path]))
    else Log(Format('添加 [%s] 到系统 PATH 失败', [Path]));
end;

procedure EnvRemovePath(Path: string);
var
    Paths: string;
    P: Integer;
begin
    if not RegQueryStringValue(HKEY_LOCAL_MACHINE, EnvironmentKey, 'Path', Paths) then
        exit;

    P := Pos(';' + Uppercase(Path) + ';', ';' + Uppercase(Paths) + ';');
    if P > 0 then
        Delete(Paths, P - 1, Length(Path) + 1)
    else
    begin
        if Pos(Uppercase(Path) + ';', Uppercase(Paths) + ';') = 1 then
            Delete(Paths, 1, Length(Path) + 1)
        else
        begin
            P := Pos(';' + Uppercase(Path), Uppercase(Paths));
            if (P > 0) and (P + Length(Path) = Length(Paths)) then
                Delete(Paths, P, Length(Path) + 1);
        end;
    end;

    RegWriteStringValue(HKEY_LOCAL_MACHINE, EnvironmentKey, 'Path', Paths);
end;

procedure BroadcastEnvironmentChange();
var
    Dummy: DWORD;
begin
    SendMessageTimeout(HWND_BROADCAST, WM_SETTINGCHANGE, 0, 0,
        SMTO_ABORTIFHUNG, 5000, Dummy);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
    if CurStep = ssPostInstall then
    begin
        if WizardIsTaskSelected('addtopath') then
        begin
            EnvAddPath(ExpandConstant('{app}'));
            BroadcastEnvironmentChange();
        end;
    end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
    if CurUninstallStep = usPostUninstall then
    begin
        EnvRemovePath(ExpandConstant('{app}'));
        BroadcastEnvironmentChange();
    end;
end;