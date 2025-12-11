// Local environment configuration for the frontend
// Adjust apiBase if your server runs on a different host/port
window.APP_CONFIG = {
  apiBase: 'http://localhost:8080',
  // Optional MCP config passed to /api/llm when the toggle is on.
  // This mirrors mcp.local.json and adds a Puppeteer MCP server via npx.
  mcpConfig: {
    version: '1.0',
    servers: {
      'austender-local': {
        transport: 'http',
        endpoint: 'http://localhost:8080/api/mcp'
      },
      puppeteer: {
        transport: 'stdio',
        command: 'npx',
        args: ['-y', '@modelcontextprotocol/server-puppeteer']
      }
    }
  }
};
