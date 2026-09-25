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

[Registry]
Root: HKLM; \
    Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Tasks: addtopath; Check: NeedsAddPath('{app}')

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "查看 ghfast 用法"; Flags: postinstall nowait skipifsilent

[Code]
const
    EnvKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

function NeedsAddPath(Param: string): Boolean;
var
    OrigPath: string;
begin
    if not RegQueryStringValue(HKEY_LOCAL_MACHINE, EnvKey, 'Path', OrigPath) then
    begin
        Result := True;
        exit;
    end;
    Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(OrigPath) + ';') = 0;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
    OrigPath, NewPath, AppDir: string;
    P: Integer;
begin
    if CurUninstallStep = usPostUninstall then
    begin
        AppDir := ExpandConstant('{app}');
        if not RegQueryStringValue(HKEY_LOCAL_MACHINE, EnvKey, 'Path', OrigPath) then
            exit;

        P := Pos(';' + Uppercase(AppDir) + ';', ';' + Uppercase(OrigPath) + ';');
        if P > 0 then
            Delete(OrigPath, P, Length(AppDir) + 1)
        else
        begin
            P := Pos(Uppercase(AppDir) + ';', Uppercase(OrigPath) + ';');
            if P = 1 then
                Delete(OrigPath, 1, Length(AppDir) + 1)
            else
            begin
                P := Pos(';' + Uppercase(AppDir), Uppercase(OrigPath));
                if (P > 0) and (P + Length(AppDir) = Length(OrigPath)) then
                    Delete(OrigPath, P, Length(AppDir) + 1);
            end;
        end;

        RegWriteExpandStringValue(HKEY_LOCAL_MACHINE, EnvKey, 'Path', OrigPath);
    end;
end;