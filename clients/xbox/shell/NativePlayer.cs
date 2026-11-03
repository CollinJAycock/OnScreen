using System;
using System.Linq;
using System.Threading.Tasks;
using Windows.Data.Json;
using Windows.Graphics.Display.Core;
using Windows.Media;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;

namespace OnScreen.Xbox
{
    /// <summary>
    /// The console's own player, for the page (../src/lib/player/nativeVideo.ts).
    /// The Xbox web view decodes neither HEVC nor AV1 nor Dolby audio and can't
    /// put the TV into HDR; this plays the page's stream (a file or an HLS
    /// playlist) in a MediaPlayerElement behind the transparent web view, so
    /// the page's player screen still draws on top. The page sends "native"
    /// messages (op: load, play, pause, seek, rate, stop); this answers with
    /// "nativeEvent" messages the page plays as media events, each tagged with
    /// the load they belong to. An HDR10 load switches the TV to an HDR10 mode,
    /// a Dolby Vision load (profile 8.1, which the console decodes as dvhe.08)
    /// to a Dolby Vision low-latency mode, else HDR10 (60 Hz either way: the
    /// console does 3:2 pulldown, and 120 Hz modes aren't for media playback);
    /// a stop goes back to the default mode. Needs the hevcPlayback capability,
    /// without which the console offers no HDR modes, and the console's Allow
    /// Dolby Vision setting for the Dolby Vision ones.
    /// </summary>
    internal sealed class NativePlayer
    {
        private readonly MediaPlayerElement element;
        private readonly CoreDispatcher dispatcher;
        private readonly Action<JsonObject> post;
        private MediaPlayer player;
        private DispatcherTimer clock;
        private double gen;
        // The display mode a load switched to: "" (the default), "hdr10", "dv".
        private string displayMode = "";
        private MediaPlaybackItem item;
        // Disposed when replaced or stopped: clearing the player's Source
        // alone left the console's HLS engine polling a finished session's
        // playlist (30 times a second, every answer a 401).
        private MediaSource source;

        /// <summary>Until when B is swallowed: the TV blanks while it resyncs
        /// after a display-mode change, and a B pressed into that blank (the
        /// screen showed nothing of the first Back yet) took the page back a
        /// second time.</summary>
        public DateTimeOffset IgnoreBackUntil { get; private set; }

        public NativePlayer(MediaPlayerElement element, Action<JsonObject> post)
        {
            this.element = element;
            dispatcher = element.Dispatcher;
            this.post = post;
        }

        public async Task HandleAsync(JsonObject m)
        {
            switch (m.GetNamedString("op", ""))
            {
                case "load":
                    await LoadAsync(m.GetNamedString("url", ""), m.GetNamedNumber("gen", 0),
                        m.GetNamedBoolean("dv", false) ? "dv" : m.GetNamedBoolean("hdr", false) ? "hdr10" : "");
                    break;
                case "play":
                    player?.Play();
                    break;
                case "pause":
                    player?.Pause();
                    break;
                case "seek":
                    if (player != null) player.PlaybackSession.Position = TimeSpan.FromSeconds(Math.Max(0, m.GetNamedNumber("t", 0)));
                    break;
                case "rate":
                    if (player != null) player.PlaybackSession.PlaybackRate = m.GetNamedNumber("rate", 1);
                    break;
                case "stop":
                    await StopAsync();
                    break;
            }
        }

        private async Task LoadAsync(string url, double loadGen, string display)
        {
            if (!Uri.TryCreate(url, UriKind.Absolute, out Uri uri)) return;
            gen = loadGen;
            await SetDisplayAsync(display);
            if (player == null) Create();
            MediaSource old = source;
            source = MediaSource.CreateFromUri(uri);
            item = new MediaPlaybackItem(source);
            player.Source = item;
            old?.Dispose();
            element.Visibility = Visibility.Visible;
            clock.Start();
        }

        public async Task StopAsync()
        {
            clock?.Stop();
            if (player != null)
            {
                player.Pause();
                player.Source = null;
            }
            source?.Dispose();
            source = null;
            element.Visibility = Visibility.Collapsed;
            item = null;
            await SetDisplayAsync("");
        }

        private void Create()
        {
            player = new MediaPlayer { AutoPlay = false };
            element.SetMediaPlayer(player);
            player.MediaOpened += (s, a) =>
            {
                MediaPlaybackSession ps = s.PlaybackSession;
                // The log reads the display, which needs the UI thread.
                MediaPlaybackItem opened = item;
                _ = dispatcher.RunAsync(CoreDispatcherPriority.Low, async () => await LogTrackAsync(opened));
                Send("loadedmetadata", o =>
                {
                    o["d"] = JsonValue.CreateNumberValue(ps.NaturalDuration.TotalSeconds);
                    o["w"] = JsonValue.CreateNumberValue(ps.NaturalVideoWidth);
                    o["h"] = JsonValue.CreateNumberValue(ps.NaturalVideoHeight);
                });
            };
            player.MediaFailed += (s, a) => Send("error", o =>
            {
                // HTMLMediaElement's MediaError codes.
                int code = a.Error == MediaPlayerError.NetworkError ? 2
                    : a.Error == MediaPlayerError.DecodingError ? 3
                    : a.Error == MediaPlayerError.SourceNotSupported ? 4 : 1;
                o["code"] = JsonValue.CreateNumberValue(code);
                o["msg"] = JsonValue.CreateStringValue(a.Error + " " + a.ErrorMessage + " 0x" + (a.ExtendedErrorCode?.HResult ?? 0).ToString("X8"));
            });
            player.MediaEnded += (s, a) => Send("ended", null);
            player.PlaybackSession.PlaybackStateChanged += (s, a) =>
            {
                string state = s.PlaybackState == MediaPlaybackState.Playing ? "playing"
                    : s.PlaybackState == MediaPlaybackState.Paused ? "paused"
                    : s.PlaybackState == MediaPlaybackState.Buffering ? "buffering" : null;
                if (state != null) Send("state", o => o["state"] = JsonValue.CreateStringValue(state));
            };
            player.PlaybackSession.SeekCompleted += (s, a) => Send("seeked", null);
            clock = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(250) };
            clock.Tick += (s, a) => Send("time", null);
        }

        /// <summary>An event for the page, with the position, the duration and
        /// the load it belongs to; posted from the UI thread.</summary>
        private void Send(string ev, Action<JsonObject> fill)
        {
            _ = dispatcher.RunAsync(CoreDispatcherPriority.Normal, () =>
            {
                if (player == null) return;
                var o = new JsonObject
                {
                    ["type"] = JsonValue.CreateStringValue("nativeEvent"),
                    ["ev"] = JsonValue.CreateStringValue(ev),
                    ["gen"] = JsonValue.CreateNumberValue(gen),
                    ["t"] = JsonValue.CreateNumberValue(player.PlaybackSession.Position.TotalSeconds),
                    ["d"] = JsonValue.CreateNumberValue(player.PlaybackSession.NaturalDuration.TotalSeconds),
                    // How far the stream reaches (a server session's playlist
                    // grows) and what is buffered, for the page's seekable /
                    // buffered.
                    ["s"] = Ranges(player.PlaybackSession.GetSeekableRanges()),
                    ["b"] = Ranges(player.PlaybackSession.GetBufferedRanges()),
                };
                fill?.Invoke(o);
                post(o);
            });
        }

        private static JsonArray Ranges(System.Collections.Generic.IReadOnlyList<MediaTimeRange> ranges)
        {
            var a = new JsonArray();
            foreach (MediaTimeRange r in ranges)
            {
                a.Add(JsonValue.CreateNumberValue(r.Start.TotalSeconds));
                a.Add(JsonValue.CreateNumberValue(r.End.TotalSeconds));
            }
            return a;
        }

        /// <summary>The display mode for a stream: "hdr10" a 4K SMPTE 2084
        /// mode, "dv" a Dolby Vision (low latency, player-led) mode, each
        /// nearest 60 Hz; "" the default mode again.</summary>
        private async Task SetDisplayAsync(string want)
        {
            try
            {
                HdmiDisplayInformation hdmi = HdmiDisplayInformation.GetForCurrentView();
                if (hdmi == null || want == displayMode) return;
                if (want == "")
                {
                    await hdmi.SetDefaultDisplayModeAsync();
                    IgnoreBackUntil = DateTimeOffset.Now.AddSeconds(2);
                    displayMode = "";
                    await LogDisplayAsync("default mode restored");
                    return;
                }
                bool dv = want == "dv";
                HdmiDisplayMode mode = hdmi.GetSupportedDisplayModes()
                    .Where(m => (dv ? m.IsDolbyVisionLowLatencySupported : m.IsSmpte2084Supported) && m.RefreshRate < 100)
                    .OrderByDescending(m => m.ResolutionHeightInRawPixels)
                    .ThenBy(m => Math.Abs(m.RefreshRate - 60))
                    .FirstOrDefault();
                bool ok = mode != null && await hdmi.RequestSetCurrentDisplayModeAsync(mode, dv ? HdmiDisplayHdrOption.DolbyVisionLowLatency : HdmiDisplayHdrOption.Eotf2084);
                if (ok)
                {
                    displayMode = want;
                    IgnoreBackUntil = DateTimeOffset.Now.AddSeconds(2);
                }
                await LogDisplayAsync((dv ? "Dolby Vision" : "HDR10") + " requested: " + (mode == null ? "no such mode" : ok ? "switched" : "refused"));
                // No Dolby Vision mode: HDR10 shows the stream's base layer.
                if (!ok && dv) await SetDisplayAsync("hdr10");
            }
            catch (Exception ex)
            {
                // The stream plays in the current mode (the console tone-maps
                // HDR10 for an SDR one).
                await LogDisplayAsync("display mode error: " + ex.Message);
            }
        }

        /// <summary>What the console made of the stream's video: subtype,
        /// profile and the Media Foundation attributes (transfer function,
        /// Dolby Vision), to the same log.</summary>
        private static async Task LogTrackAsync(MediaPlaybackItem it)
        {
            try
            {
                if (it == null || it.VideoTracks.Count == 0) { await LogDisplayAsync("video track: none listed"); return; }
                Windows.Media.MediaProperties.VideoEncodingProperties v = it.VideoTracks[0].GetEncodingProperties();
                var sb = new System.Text.StringBuilder("video track: " + v.Subtype + " profile " + v.ProfileId + " " + v.Width + "x" + v.Height);
                foreach (var kv in v.Properties) sb.Append("; " + kv.Key + "=" + kv.Value);
                await LogDisplayAsync(sb.ToString());
            }
            catch (Exception ex)
            {
                await LogDisplayAsync("video track error: " + ex.Message);
            }
        }

        /// <summary>The display switches, with what the console then reports,
        /// to LocalState\display-switches.txt (readable over Device Portal):
        /// a TV's own HDR indicator is easy to miss.</summary>
        private static async Task LogDisplayAsync(string what)
        {
            try
            {
                string kind = Windows.Graphics.Display.DisplayInformation.GetForCurrentView().GetAdvancedColorInfo().CurrentAdvancedColorKind.ToString();
                HdmiDisplayMode m = HdmiDisplayInformation.GetForCurrentView()?.GetCurrentDisplayMode();
                string line = DateTimeOffset.Now.ToString("o") + " " + what + "; colour " + kind +
                    (m == null ? "" : "; " + m.ResolutionWidthInRawPixels + "x" + m.ResolutionHeightInRawPixels + "@" + m.RefreshRate.ToString("0.###") +
                        " " + m.ColorSpace + " " + m.BitsPerPixel + "bpp" + (m.IsSmpte2084Supported ? " 2084" : "")) + "\r\n";
                Windows.Storage.StorageFile f = await Windows.Storage.ApplicationData.Current.LocalFolder.CreateFileAsync(
                    "display-switches.txt", Windows.Storage.CreationCollisionOption.OpenIfExists);
                // Kept short: the last few dozen switches are plenty.
                if ((await f.GetBasicPropertiesAsync()).Size > 16 * 1024) await Windows.Storage.FileIO.WriteTextAsync(f, "");
                await Windows.Storage.FileIO.AppendTextAsync(f, line);
            }
            catch
            {
                // Diagnostics only.
            }
        }
    }
}
