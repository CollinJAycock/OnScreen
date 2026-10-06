using System;
using Windows.ApplicationModel;
using Windows.ApplicationModel.Activation;
using Windows.UI.ViewManagement;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;

namespace OnScreen.Xbox
{
    sealed partial class App : Application
    {
        public App()
        {
            // A crash leaves its exception in LocalState\crash.txt: on a
            // console there is no other way to read it.
            UnhandledException += (s, e) => WriteCrash(e.Exception);
            try
            {
                InitializeComponent();
                // No mouse cursor on Xbox: the controller drives the page's
                // own focus (the TV app's D-pad navigation), as on a TV remote.
                RequiresPointerMode = ApplicationRequiresPointerMode.WhenRequested;
            }
            catch (Exception ex)
            {
                WriteCrash(ex);
                throw;
            }
            // Autoplay with sound: the player starts a title the user chose
            // with a controller press, which WebView2 doesn't count as a page
            // gesture. Read by WebView2 when it starts, so set before any
            // control is created.
            string args = "--autoplay-policy=no-user-gesture-required";
#if DEBUG || DEVTOOLS
            // DevTools protocol for testing the page from a PC (Debug, or a
            // local test build: build.ps1 -DevTools). Never in a package for
            // a console or the Store.
            args += " --remote-debugging-port=9229";
#endif
            Environment.SetEnvironmentVariable("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", args);
        }

        internal static void WriteCrash(Exception ex)
        {
            try
            {
                string path = System.IO.Path.Combine(Windows.Storage.ApplicationData.Current.LocalFolder.Path, "crash.txt");
                System.IO.File.WriteAllText(path, DateTimeOffset.Now + "\r\n" + ex);
            }
            catch
            {
                // Nowhere left to report it.
            }
        }

        protected override void OnLaunched(LaunchActivatedEventArgs e)
        {
            // Edge to edge: the TV app keeps its own TV-safe margins, so the
            // system's overscan border would only shrink it.
            ApplicationView.GetForCurrentView().SetDesiredBoundsMode(ApplicationViewBoundsMode.UseCoreWindow);

            if (!(Window.Current.Content is Frame root))
            {
                root = new Frame();
                Window.Current.Content = root;
            }
            if (root.Content == null)
            {
                root.Navigate(typeof(MainPage), e.Arguments);
            }
            Window.Current.Activate();
        }
    }
}
