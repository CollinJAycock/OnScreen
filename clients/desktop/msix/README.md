# Microsoft Store package (desktop)

The Windows desktop app ships in the Microsoft Store as a full-trust MSIX in
the **same Store product as the Xbox app** ("OnScreen", Store ID
`9N58M48F5XMW`). Both packages use the identity `OnScreen.OnScreen` and
publisher `CN=4F8B1511-8473-4887-9ED7-421BC42D615E`. They differ in device
family:

| Package | Device family | Source |
|---|---|---|
| Xbox (UWP, the TV app) | `Windows.Universal` | `clients/xbox/shell` |
| Desktop (this, Tauri, full trust) | `Windows.Desktop` | `clients/desktop` + `AppxManifest.xml` here |

On a PC the Store offers the highest-versioned package that runs there. Today
that is the desktop package (2.7.0.0 against the Xbox 0.1.x). Consoles keep the
Xbox package.

## Build

```powershell
.\build-msix.ps1              # dist\OnScreen-Desktop_<ver>_x64.msix
.\build-msix.ps1 -Register    # also register the staged layout on this PC (Developer Mode)
# or, store-named into clients\packages:
..\..\build-packages.ps1 -Target microsoft-desktop
```

The script:

1. Builds the release exe with `..\build.ps1 -NoBundle`, which holds the
   pinned tauri-cli check.
2. Stages the exe with this manifest, filling in `{VERSION}` from
   `tauri.conf.json` plus `.0`; the Store keeps the 4th part.
3. Copies the logos from `clients/xbox/shell/Assets`, so both packages carry
   the same artwork.
4. Generates one `resources.pri`.
5. Packs the package and signs it with the Xbox shell's local test
   certificate. Partner Center re-signs it.

There is no updater in the app: the Store updates it.

## Partner Center (owner)

1. Start a new submission of the existing product. On **Packages**, add
   `OnScreen-MicrosoftStore-Windows-<ver>.msix` next to the Xbox package.
   Under device family availability, leave Windows 10/11 Desktop on for it.
2. **runFullTrust** is a restricted capability. Partner Center asks why; the
   answer is that it is a Win32 desktop app: Tauri, with a WebView2 UI and
   native audio (WASAPI).
3. **Store listing:** add PC screenshots (1920×1080) next to the Xbox ones,
   and make the description cover both (PC app plus TV app on Xbox).
4. Age rating and pricing are product-wide, so nothing changes there.

## Differences from the MSI/NSIS install

- **Data.** Settings written by the packaged app go to the package's private
  AppData; existing files from an MSI install are still readable. The keyring
  (Windows Credential Manager) is shared.
- **No Start-menu shortcut AUMID of its own.** The app's AppUserModelID is the
  package's, `OnScreen.OnScreen_d8tnyqrgg61zm!App`.
