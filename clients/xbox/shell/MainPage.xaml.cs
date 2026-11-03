using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Web.WebView2.Core;
using Windows.Data.Json;
using Windows.Graphics.Display;
using Windows.Graphics.Display.Core;
using Windows.Security.ExchangeActiveSyncProvisioning;
using Windows.Storage;
using Windows.System.Profile;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Navigation;
using Windows.Web.Http;

namespace OnScreen.Xbox
{
    /// <summary>
    /// The whole shell: ask for the OnScreen server, then show its TV app
    /// (<server>/tvapp/) in a WebView2. The page tells the shell when to exit
    /// and when to change server; the shell tells the page the console model
    /// and what the TV takes (query string, see ../src/lib/shell.ts), and
    /// passes on the system's Back request.
    /// </summary>
    public sealed partial class MainPage : Page
    {
        private const string ServerKey = "server";
        private const string TvAppPath = "/tvapp/";
        // The server's page when it was built without the TV app
        // (internal/tvui/placeholder).
        private const string PlaceholderMarker = "TV app isn't included";

        private readonly ApplicationDataContainer settings = ApplicationData.Current.LocalSettings;
        private bool webReady;
        // Until the page reports that the controller reaches it as key
        // events ("nativeKeys"), the system's Back request is the only way B
        // gets to it, so it is forwarded as a "back" message.
        private bool forwardBack = true;
        private CancellationTokenSource pendingBack;
        // The console hands the web view no controller key events (the
        // page's key log showed only its own Gamepad API polling, code 0),
        // so the shell passes the window's gamepad keys on as "key"
        // messages. Off for good once the page reports key events of its own
        // ("webKeys"), or both would arrive.
        private bool forwardKeys = true;
        // The console's own player for what the web view can't play (NativePlayer).
        private NativePlayer native;

        public MainPage()
        {
            InitializeComponent();
        }

        protected override async void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            SystemNavigationManager.GetForCurrentView().BackRequested += OnBackRequested;
            CoreWindow window = Window.Current.CoreWindow;
            window.KeyDown += (s, a) => ForwardKey(a, "keydown");
            window.KeyUp += (s, a) => ForwardKey(a, "keyup");
            native = new NativePlayer(Native, PostToPage);
            _ = WriteDisplayReportAsync();
            if (settings.Values[ServerKey] is string saved && saved.Length > 0)
            {
                ServerBox.Text = saved;
                await OpenAsync(saved);
            }
            else
            {
                ShowPrompt(null);
            }
        }

        // ── Server prompt ───────────────────────────────────────────────────

        private void ShowPrompt(string error)
        {
            Web.Visibility = Visibility.Collapsed;
            Prompt.Visibility = Visibility.Visible;
            Status.Text = error ?? "";
            ConnectButton.IsEnabled = true;
            ServerBox.Focus(FocusState.Programmatic);
        }

        private async void Connect_Click(object sender, RoutedEventArgs e) => await ConnectAsync();

        private async void ServerBox_KeyDown(object sender, KeyRoutedEventArgs e)
        {
            if (e.Key == Windows.System.VirtualKey.Enter)
            {
                e.Handled = true;
                await ConnectAsync();
            }
        }

        private async Task ConnectAsync()
        {
            string typed = ServerBox.Text.Trim().TrimEnd('/');
            if (typed.Length == 0)
            {
                Status.Text = "Enter your server's address.";
                return;
            }
            ConnectButton.IsEnabled = false;
            Status.Text = "Checking the server…";
            string lastError = "The server didn't answer.";
            foreach (string candidate in Candidates(typed))
            {
                string error = await CheckServerAsync(candidate);
                if (error == null)
                {
                    settings.Values[ServerKey] = candidate;
                    await OpenAsync(candidate);
                    return;
                }
                lastError = error;
            }
            ConnectButton.IsEnabled = true;
            Status.Text = lastError;
        }

        /// <summary>The addresses to try: as typed when it has a scheme,
        /// else https first, then http (a LAN server is usually plain http).</summary>
        private static IEnumerable<string> Candidates(string typed)
        {
            if (typed.StartsWith("http://", StringComparison.OrdinalIgnoreCase) ||
                typed.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            {
                yield return typed;
                yield break;
            }
            yield return "https://" + typed;
            yield return "http://" + typed;
        }

        /// <summary>null when <paramref name="server"/> serves the TV app;
        /// otherwise what to tell the user.</summary>
        private static async Task<string> CheckServerAsync(string server)
        {
            if (!Uri.TryCreate(server + TvAppPath, UriKind.Absolute, out Uri uri))
            {
                return "That isn't a server address.";
            }
            try
            {
                using (var client = new HttpClient())
                using (var cts = new CancellationTokenSource(TimeSpan.FromSeconds(8)))
                {
                    HttpResponseMessage resp = await client.GetAsync(uri).AsTask(cts.Token);
                    if (!resp.IsSuccessStatusCode)
                    {
                        return $"{server} answered {(int)resp.StatusCode}: is it an OnScreen server?";
                    }
                    string body = await resp.Content.ReadAsStringAsync();
                    if (body.Contains(PlaceholderMarker))
                    {
                        return "This OnScreen server doesn't include the TV app yet. Update the server, then try again.";
                    }
                    return null;
                }
            }
            catch (TaskCanceledException)
            {
                return $"{server} didn't answer in time.";
            }
            catch (Exception ex)
            {
                return $"Couldn't reach {server}: {ex.Message}";
            }
        }

        // ── The TV app ──────────────────────────────────────────────────────

        private async Task OpenAsync(string server)
        {
            Prompt.Visibility = Visibility.Collapsed;
            Web.Visibility = Visibility.Visible;
            try
            {
                if (!webReady)
                {
                    // Transparent where the page is: the native player draws
                    // behind it, and the page clears its own background while
                    // that plays.
                    Environment.SetEnvironmentVariable("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "00000000");
                    await Web.EnsureCoreWebView2Async();
                    CoreWebView2Settings s = Web.CoreWebView2.Settings;
                    s.AreDefaultContextMenusEnabled = false;
                    s.IsZoomControlEnabled = false;
                    s.IsStatusBarEnabled = false;
                    s.AreBrowserAcceleratorKeysEnabled = false;
#if !DEBUG && !DEVTOOLS
                    s.AreDevToolsEnabled = false;
#endif
                    Web.CoreWebView2.WebMessageReceived += OnWebMessage;
                    Web.CoreWebView2.NavigationCompleted += OnNavigationCompleted;
                    // A reload or another server: nothing left drives the native player.
                    Web.CoreWebView2.NavigationStarting += async (_, __) => await native.StopAsync();
                    webReady = true;
                }
                forwardBack = true;
                Web.CoreWebView2.Navigate(TvAppUrl(server));
                Web.Focus(FocusState.Programmatic);
            }
            catch (Exception ex)
            {
                ShowPrompt($"Couldn't start the web view: {ex.Message}");
            }
        }

        private void OnNavigationCompleted(CoreWebView2 sender, CoreWebView2NavigationCompletedEventArgs args)
        {
            if (args.IsSuccess || Prompt.Visibility == Visibility.Visible) return;
            ShowPrompt($"Couldn't load the TV app ({args.WebErrorStatus}). Check the server, or enter another.");
        }

        /// <summary><server>/tvapp/index.html with what only native code
        /// knows: the console model and what the TV takes.</summary>
        private static string TvAppUrl(string server)
        {
            var q = new List<string> { "shell=xbox", "device=" + Uri.EscapeDataString(DeviceModel()), "native=1" };
            DisplayCaps(out bool? uhd, out bool? hdr);
            if (uhd.HasValue) q.Add("uhd=" + (uhd.Value ? "1" : "0"));
            if (hdr.HasValue) q.Add("hdr=" + (hdr.Value ? "1" : "0"));
            return server + TvAppPath + "index.html?" + string.Join("&", q);
        }

        /// <summary>"Xbox Series X", "Xbox One S"…; "Windows" on a PC.</summary>
        private static string DeviceModel()
        {
            try
            {
                if (AnalyticsInfo.VersionInfo.DeviceFamily != "Windows.Xbox") return "Windows";
                string name = new EasClientDeviceInformation().SystemProductName;
                return string.IsNullOrWhiteSpace(name) ? "Xbox" : name.Trim();
            }
            catch
            {
                return "Xbox";
            }
        }

        /// <summary>From the HDMI output (Xbox only; null on a PC): whether
        /// the TV takes 2160p, and HDR10 (SMPTE ST 2084) in any mode.</summary>
        private static void DisplayCaps(out bool? uhd, out bool? hdr)
        {
            uhd = null;
            hdr = null;
            try
            {
                HdmiDisplayInformation hdmi = HdmiDisplayInformation.GetForCurrentView();
                if (hdmi == null) return;
                IReadOnlyList<HdmiDisplayMode> modes = hdmi.GetSupportedDisplayModes();
                uhd = modes.Any(m => m.ResolutionHeightInRawPixels >= 2160);
                hdr = modes.Any(m => m.IsSmpte2084Supported);
            }
            catch
            {
                // No HDMI information: the page falls back to the browser's.
            }
        }

        /// <summary>Everything the display APIs say (HDMI modes and their HDR
        /// flags, advanced colour) to LocalState\display.txt, readable over
        /// Device Portal: which API answers a TV's HDR10 truthfully differs
        /// by console and setting.</summary>
        private static async Task WriteDisplayReportAsync()
        {
            var sb = new StringBuilder();
            sb.AppendLine("written " + DateTimeOffset.Now.ToString("o"));
            try
            {
                HdmiDisplayInformation hdmi = HdmiDisplayInformation.GetForCurrentView();
                if (hdmi == null)
                {
                    sb.AppendLine("hdmi: none");
                }
                else
                {
                    sb.AppendLine("current: " + DescribeMode(hdmi.GetCurrentDisplayMode()));
                    IReadOnlyList<HdmiDisplayMode> modes = hdmi.GetSupportedDisplayModes();
                    sb.AppendLine("modes: " + modes.Count + ", with SMPTE 2084: " + modes.Count(m => m.IsSmpte2084Supported) + ", Dolby Vision low latency: " + modes.Count(m => m.IsDolbyVisionLowLatencySupported));
                    foreach (HdmiDisplayMode m in modes) sb.AppendLine("  " + DescribeMode(m));
                }
            }
            catch (Exception ex)
            {
                sb.AppendLine("hdmi error: " + ex.Message);
            }
            try
            {
                AdvancedColorInfo aci = DisplayInformation.GetForCurrentView().GetAdvancedColorInfo();
                sb.AppendLine("advanced colour: kind " + aci.CurrentAdvancedColorKind +
                    ", HDR10 metadata " + aci.IsHdrMetadataFormatCurrentlySupported(HdrMetadataFormat.Hdr10) +
                    ", max " + aci.MaxLuminanceInNits + " nits, SDR white " + aci.SdrWhiteLevelInNits + " nits");
            }
            catch (Exception ex)
            {
                sb.AppendLine("advanced colour error: " + ex.Message);
            }
            try
            {
                StorageFile f = await ApplicationData.Current.LocalFolder.CreateFileAsync("display.txt", CreationCollisionOption.ReplaceExisting);
                await FileIO.WriteTextAsync(f, sb.ToString());
            }
            catch
            {
                // Diagnostics only.
            }
        }

        private static string DescribeMode(HdmiDisplayMode m) =>
            m == null ? "none" :
            m.ResolutionWidthInRawPixels + "x" + m.ResolutionHeightInRawPixels + "@" + m.RefreshRate.ToString("0.###") +
            " " + m.ColorSpace + " " + m.PixelEncoding + " " + m.BitsPerPixel + "bpp" +
            (m.IsSmpte2084Supported ? " 2084" : "") + (m.Is2086MetadataSupported ? " 2086" : "") +
            (m.IsSdrLuminanceSupported ? " sdr" : "") + (m.IsDolbyVisionLowLatencySupported ? " dv-ll" : "") + (m.StereoEnabled ? " stereo" : "");

        /// <summary>A message for the page (UI thread).</summary>
        private void PostToPage(JsonObject o)
        {
            if (webReady && Web.CoreWebView2 != null) Web.CoreWebView2.PostWebMessageAsJson(o.Stringify());
        }

        private async void OnWebMessage(CoreWebView2 sender, CoreWebView2WebMessageReceivedEventArgs args)
        {
            JsonObject message;
            string type;
            try
            {
                message = JsonObject.Parse(args.WebMessageAsJson);
                type = message.GetNamedString("type", "");
            }
            catch
            {
                return;
            }
            switch (type)
            {
                case "native":
                    await native.HandleAsync(message);
                    // The page keeps the keyboard focus whatever the player did
                    // (B is the page's, through the web view).
                    if (Web.Visibility == Visibility.Visible) Web.Focus(FocusState.Programmatic);
                    break;
                case "exit":
                    Application.Current.Exit();
                    break;
                case "changeServer":
                    await native.StopAsync();
                    settings.Values.Remove(ServerKey);
                    Web.CoreWebView2.Navigate("about:blank");
                    await Task.Yield();
                    ShowPrompt(null);
                    break;
                case "nativeKeys":
                    forwardBack = false;
                    pendingBack?.Cancel();
                    break;
                case "webKeys":
                    forwardKeys = false;
                    break;
            }
        }

        // ── Controller keys ─────────────────────────────────────────────────

        /// <summary>A gamepad key (VK_GAMEPAD_*, 195-218) to the page, which
        /// plays it as the key event a web view would have had
        /// (../src/lib/shell.ts). If B comes this way too, the system's Back
        /// request it also raises is no longer forwarded; a Series X raises
        /// only the Back request for B, so that stays the way B arrives.</summary>
        private void ForwardKey(KeyEventArgs e, string type)
        {
            int code = (int)e.VirtualKey;
            if (code < 195 || code > 218) return;
            if (!forwardKeys || Web.Visibility != Visibility.Visible || !webReady) return;
            if (code == (int)Windows.System.VirtualKey.GamepadB && DateTimeOffset.Now < native.IgnoreBackUntil)
            {
                // The TV is resyncing after a display-mode change (NativePlayer).
                e.Handled = true;
                return;
            }
            if (code == (int)Windows.System.VirtualKey.GamepadB)
            {
                forwardBack = false;
                pendingBack?.Cancel();
            }
            bool repeat = type == "keydown" && e.KeyStatus.WasKeyDown;
            Web.CoreWebView2.PostWebMessageAsJson(
                "{\"type\":\"key\",\"event\":\"" + type + "\",\"code\":" + code +
                ",\"repeat\":" + (repeat ? "true" : "false") + "}");
            e.Handled = true;
        }

        // ── Back ────────────────────────────────────────────────────────────

        private async void OnBackRequested(object sender, BackRequestedEventArgs e)
        {
            if (Web.Visibility != Visibility.Visible || !webReady) return; // the prompt: the system's Back
            // The app is the page's to leave (its exit popup), never the
            // system's on B.
            e.Handled = true;
            if (!forwardBack || DateTimeOffset.Now < native.IgnoreBackUntil) return;
            // B may also reach the page as a key event; give the page a moment
            // to say so ("nativeKeys" cancels this) before playing it as Back.
            pendingBack?.Cancel();
            var cts = new CancellationTokenSource();
            pendingBack = cts;
            try
            {
                await Task.Delay(150, cts.Token);
            }
            catch (TaskCanceledException)
            {
                return;
            }
            if (forwardBack) Web.CoreWebView2.PostWebMessageAsJson("{\"type\":\"back\"}");
        }
    }
}
