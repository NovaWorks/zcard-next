# Image upload regression fixtures

These original 32×32 solid-color fixtures contain no customer images or metadata.
They were generated with Pillow 12.3.0 (JPEG, PNG, BMP, TIFF, ICO, AVIF,
WebP and GIF); `still.svg` is a red rectangle. `still.heic` was converted from
`still.png` with macOS `sips -s format heic`.

Animated fixtures contain red and blue frames, 120/240 ms durations, and a loop
count of 3. WebP covers lossy alpha, opaque and lossless encodings. The additional
APNG fixture has a separate default image (`default_image=True`).

Tests deliberately upload every format as `customer.jpg` / `image/jpeg` and
verify the actual HTTP upload response, stored metadata and public image response.
They verify GIF frame data/timing and byte-preserving APNG/WebP storage, and reject
truncated files, active SVG, and excessive image dimensions.
