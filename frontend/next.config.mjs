/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
  images: { unoptimized: true },
  // Restore the scroll position when the user hits Back/Forward (e.g.
  // returning to /traces after viewing a single trace). Without this
  // Next.js's App Router scrolls to the top on every navigation.
  experimental: {
    scrollRestoration: true,
  },
  // In dev, proxy API calls to the Qmetry backend.
  // (rewrites only run in `next dev`, ignored on static export.)
  async rewrites() {
    return [
      { source: '/api/:path*', destination: 'http://localhost:8088/api/:path*' },
    ];
  },
};
export default nextConfig;
