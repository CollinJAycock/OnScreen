// Single source of truth for "given an item type, where does clicking
// it route?". Used by the hub, library landing pages, and search so a
// new media type only needs one mapping update instead of three.
//
// Container types (artist, album, photo, podcast, book, book_author,
// book_series, audiobook) route to their typed detail page — these
// rows often have no direct file (just children), so /watch would
// render "No playable file found". Everything else is a leaf with a
// stream and lands on /watch.
export function itemHref(type: string, id: string): string {
  switch (type) {
    case 'artist':       return `/artists/${id}`;
    case 'album':        return `/albums/${id}`;
    case 'photo':        return `/photos/${id}`;
    case 'podcast':      return `/podcasts/${id}`;
    case 'book':         return `/books/${id}`;
    case 'book_author':  return `/authors/${id}`;
    case 'book_series':  return `/series/${id}`;
    case 'audiobook':    return `/audiobooks/${id}`;
    default:             return `/watch/${id}`;
  }
}
