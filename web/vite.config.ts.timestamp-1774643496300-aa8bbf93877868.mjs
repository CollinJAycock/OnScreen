// vite.config.ts
import { sveltekit } from "file:///C:/Users/colli/Desktop/OnScreen/web/node_modules/@sveltejs/kit/src/exports/vite/index.js";
import { defineConfig } from "file:///C:/Users/colli/Desktop/OnScreen/web/node_modules/vite/dist/node/index.js";
var vite_config_default = defineConfig({
  plugins: [sveltekit()],
  server: {
    port: 5173,
    // In dev mode, proxy API and Plex requests to the Go server.
    proxy: {
      "/api": "http://localhost:7070",
      "/library": "http://localhost:7070",
      "/identity": "http://localhost:7070",
      "/health": "http://localhost:7070",
      "/artwork": "http://localhost:7070",
      "/photo": "http://localhost:7070"
    }
  }
});
export {
  vite_config_default as default
};
//# sourceMappingURL=data:application/json;base64,ewogICJ2ZXJzaW9uIjogMywKICAic291cmNlcyI6IFsidml0ZS5jb25maWcudHMiXSwKICAic291cmNlc0NvbnRlbnQiOiBbImNvbnN0IF9fdml0ZV9pbmplY3RlZF9vcmlnaW5hbF9kaXJuYW1lID0gXCJDOlxcXFxVc2Vyc1xcXFxjb2xsaVxcXFxEZXNrdG9wXFxcXE9uU2NyZWVuXFxcXHdlYlwiO2NvbnN0IF9fdml0ZV9pbmplY3RlZF9vcmlnaW5hbF9maWxlbmFtZSA9IFwiQzpcXFxcVXNlcnNcXFxcY29sbGlcXFxcRGVza3RvcFxcXFxPblNjcmVlblxcXFx3ZWJcXFxcdml0ZS5jb25maWcudHNcIjtjb25zdCBfX3ZpdGVfaW5qZWN0ZWRfb3JpZ2luYWxfaW1wb3J0X21ldGFfdXJsID0gXCJmaWxlOi8vL0M6L1VzZXJzL2NvbGxpL0Rlc2t0b3AvT25TY3JlZW4vd2ViL3ZpdGUuY29uZmlnLnRzXCI7aW1wb3J0IHsgc3ZlbHRla2l0IH0gZnJvbSAnQHN2ZWx0ZWpzL2tpdC92aXRlJztcbmltcG9ydCB7IGRlZmluZUNvbmZpZyB9IGZyb20gJ3ZpdGUnO1xuXG5leHBvcnQgZGVmYXVsdCBkZWZpbmVDb25maWcoe1xuICBwbHVnaW5zOiBbc3ZlbHRla2l0KCldLFxuICBzZXJ2ZXI6IHtcbiAgICBwb3J0OiA1MTczLFxuICAgIC8vIEluIGRldiBtb2RlLCBwcm94eSBBUEkgYW5kIFBsZXggcmVxdWVzdHMgdG8gdGhlIEdvIHNlcnZlci5cbiAgICBwcm94eToge1xuICAgICAgJy9hcGknOiAnaHR0cDovL2xvY2FsaG9zdDo3MDcwJyxcbiAgICAgICcvbGlicmFyeSc6ICdodHRwOi8vbG9jYWxob3N0OjcwNzAnLFxuICAgICAgJy9pZGVudGl0eSc6ICdodHRwOi8vbG9jYWxob3N0OjcwNzAnLFxuICAgICAgJy9oZWFsdGgnOiAnaHR0cDovL2xvY2FsaG9zdDo3MDcwJyxcbiAgICAgICcvYXJ0d29yayc6ICdodHRwOi8vbG9jYWxob3N0OjcwNzAnLFxuICAgICAgJy9waG90byc6ICdodHRwOi8vbG9jYWxob3N0OjcwNzAnXG4gICAgfVxuICB9XG59KTtcbiJdLAogICJtYXBwaW5ncyI6ICI7QUFBdVMsU0FBUyxpQkFBaUI7QUFDalUsU0FBUyxvQkFBb0I7QUFFN0IsSUFBTyxzQkFBUSxhQUFhO0FBQUEsRUFDMUIsU0FBUyxDQUFDLFVBQVUsQ0FBQztBQUFBLEVBQ3JCLFFBQVE7QUFBQSxJQUNOLE1BQU07QUFBQTtBQUFBLElBRU4sT0FBTztBQUFBLE1BQ0wsUUFBUTtBQUFBLE1BQ1IsWUFBWTtBQUFBLE1BQ1osYUFBYTtBQUFBLE1BQ2IsV0FBVztBQUFBLE1BQ1gsWUFBWTtBQUFBLE1BQ1osVUFBVTtBQUFBLElBQ1o7QUFBQSxFQUNGO0FBQ0YsQ0FBQzsiLAogICJuYW1lcyI6IFtdCn0K
