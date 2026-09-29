// Leaflet 1.9's package.json points `main` at its UMD build and has no
// `module` field, so a bare 'leaflet' import goes through CommonJS interop.
// photoMapLeaflet.ts imports the ESM build the package also ships instead;
// this gives that path the package's own types (@types/leaflet).
declare module 'leaflet/dist/leaflet-src.esm.js' {
  export * from 'leaflet';
}
