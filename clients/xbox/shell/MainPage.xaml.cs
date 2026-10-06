using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Web.WebView2.Core;
using Windows.Data.Json;
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

        public MainPage()
        {
            InitializeComponent();
        }

        protected override async void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            SystemNavigationManager.GetForCurrentView().BackRequested += OnBackRequested;
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
            var q = new List<string> { "shell=xbox", "device=" + Uri.EscapeDataString(DeviceModel()) };
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

        private async void OnWebMessage(CoreWebView2 sender, CoreWebView2WebMessageReceivedEventArgs args)
        {
            string type;
            try
            {
                type = JsonObject.Parse(args.WebMessageAsJson).GetNamedString("type", "");
            }
            catch
            {
                return;
            }
            switch (type)
            {
                case "exit":
                    Application.Current.Exit();
                    break;
                case "changeServer":
                    settings.Values.Remove(ServerKey);
                    Web.CoreWebView2.Navigate("about:blank");
                    await Task.Yield();
                    ShowPrompt(null);
                    break;
                case "nativeKeys":
                    forwardBack = false;
                    pendingBack?.Cancel();
                    break;
            }
        }

        // ── Back ────────────────────────────────────────────────────────────

        private async void OnBackRequested(object sender, BackRequestedEventArgs e)
        {
            if (Web.Visibility != Visibility.Visible || !webReady) return; // the prompt: the system's Back
            // The app is the page's to leave (its exit popup), never the
            // system's on B.
            e.Handled = true;
            if (!forwardBack) return;
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
