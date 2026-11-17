/** @type {import('next').NextConfig} */
// REST API v1 lives at :8081 (Gin router — dashboard + v1 endpoints).
// MCP JSON-RPC + WebSocket live at :10600.
const REST_BACKEND  = process.env.BACKEND_URL     ?? 'http://localhost:8081';
const MCP_BACKEND   = process.env.MCP_BACKEND_URL ?? 'http://localhost:10600';

const createNextIntlPlugin = require('next-intl/plugin');

const withNextIntl = createNextIntlPlugin('./src/i18n/request.ts');

const nextConfig = {
  allowedDevOrigins: ['127.0.0.1', 'localhost'],
  output: 'standalone',
  reactStrictMode: true,
  images: {
    unoptimized: true,
    remotePatterns: [
      { protocol: 'https', hostname: '**.amazonaws.com' },
      { protocol: 'https', hostname: '**.rantipay.org' },
      { protocol: 'http', hostname: 'localhost' },
      { protocol: 'http', hostname: '127.0.0.1' },
    ],
  },
  typescript: { ignoreBuildErrors: false },
  // Proxy all /api/* and /ws requests to the Go backend.
  // This eliminates CORS in the browser — the browser only talks to Next.js (:3002).
  async rewrites() {
    return [
      // AI bot status lives only on the MCP server (:10600). Listed before the
      // generic /api rule because rewrites match in order. Read-only.
      {
        source: '/api/v1/ai-bot/status',
        destination: `${MCP_BACKEND}/api/v1/ai-bot/status`,
      },
      // REST API at :8081
      {
        source: '/api/:path*',
        destination: `${REST_BACKEND}/api/:path*`,
      },
      // WebSockets
      {
        source: '/ws',
        destination: `${MCP_BACKEND}/ws`,
      },
      // Health + tools at MCP server
      {
        source: '/health',
        destination: `${MCP_BACKEND}/health`,
      },
    ];
  },
};

module.exports = withNextIntl(nextConfig);
