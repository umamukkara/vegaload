// @ts-check
/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    {
      type: 'category',
      label: 'Why VegaLoad',
      items: ['why-vegaload/index', 'why-vegaload/who-is-it-for'],
    },
    'features',
    {
      type: 'category',
      label: 'Compare',
      items: [
        'compare/index',
        'compare/k6',
        'compare/jmeter',
        'compare/locust',
        'compare/gatling',
        'compare/simple-tools',
        'compare/migrating',
      ],
    },
    'scale-with-harness',
    {
      type: 'category',
      label: 'Get started',
      link: {type: 'doc', id: 'get-started/install'},
      items: [
        'get-started/install',
        'get-started/first-test',
        'get-started/read-the-report',
        'get-started/cursor',
        'get-started/claude-code',
        'get-started/claude-desktop',
      ],
    },
    {
      type: 'category',
      label: 'AI agents',
      items: [
        'ai-agents/how-it-works',
        'ai-agents/mcp-tools',
        'ai-agents/safety',
        'ai-agents/prompts',
      ],
    },
    {
      type: 'category',
      label: 'Guides',
      items: ['guides/index'],
    },
    {
      type: 'category',
      label: 'Cookbook',
      items: ['cookbook/index'],
    },
    {
      type: 'category',
      label: 'CI/CD',
      items: ['ci-cd/index'],
    },
    {
      type: 'category',
      label: 'Reference',
      items: ['reference/index'],
    },
    {
      type: 'category',
      label: 'Help',
      items: ['help/troubleshooting', 'help/faq'],
    },
  ],
};

module.exports = sidebars;
