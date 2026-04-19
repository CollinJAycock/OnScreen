declare global {
  namespace App {}

  interface Window {
    webOS?: {
      platformBack?: () => void;
      deviceInfo?: (cb: (info: unknown) => void) => void;
    };
  }
}

export {};
