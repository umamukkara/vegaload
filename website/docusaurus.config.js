// @ts-check
const prismThemes = require('prism-react-renderer').themes;

const url = process.env.DOCS_URL || 'https://vegaload.github.io';
const baseUrl = process.env.DOCS_BASE_URL || '/vegaload/';
// Hidden until launch. Only the string "false" turns indexing on.
const noIndex = process.env.DOCS_NO_INDEX !== 'false';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'VegaLoad docs',
  tagline: 'Load testing for agentic developers.',
  url,
  baseUrl,
  trailingSlash: false,
  organizationName: 'vegaload',
  projectName: 'vegaload',
  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },
  noIndex,
  favicon: 'img/favicon.svg',
  headTags: [
    {
      tagName: 'link',
      attributes: {rel: 'preconnect', href: 'https://fonts.googleapis.com'},
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.gstatic.com',
        crossorigin: 'anonymous',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Geist:wght@400;500;600&family=IBM+Plex+Mono:wght@400;500&family=Space+Grotesk:wght@500;600;700&display=swap',
      },
    },
  ],
  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/vegaload/vegaload/edit/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],
  plugins: [
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        language: ['en'],
        indexDocs: true,
        indexBlog: false,
        docsRouteBasePath: '/',
        // The site is noindex until launch. Search still indexes the pages.
        forceIgnoreNoIndex: true,
      },
    ],
  ],
  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
      metadata: [
        {name: 'description', content: 'Load testing for agentic developers.'},
      ],
      navbar: {
        title: 'VegaLoad',
        logo: {
          alt: 'VegaLoad',
          src: 'img/logo-light.svg',
          srcDark: 'img/logo-dark.svg',
        },
        items: [
          {
            type: 'dropdown',
            label: 'Learn',
            position: 'left',
            items: [
              {to: '/why-vegaload', label: 'Why VegaLoad'},
              {to: '/features', label: 'Features'},
              {to: '/compare', label: 'Compare'},
              {to: '/scale-with-harness', label: 'Scale with Harness'},
            ],
          },
          {to: '/get-started/install', label: 'Get started', position: 'left'},
          {to: '/guides', label: 'Guides', position: 'left'},
          {to: '/ai-agents/how-it-works', label: 'AI agents', position: 'left'},
          {to: '/cookbook', label: 'Cookbook', position: 'left'},
          {
            href: 'https://github.com/vegaload/vegaload',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        copyright:
          'Open source, sponsored by <a href="https://www.harness.io">Harness</a>. <a href="https://github.com/vegaload/vegaload">GitHub</a>. <a href="https://github.com/vegaload/vegaload/blob/main/LICENSE">Apache 2.0</a>.',
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
      },
    }),
};

module.exports = config;
