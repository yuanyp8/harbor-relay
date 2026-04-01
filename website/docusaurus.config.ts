import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';

const config: Config = {
  title: 'Harbor Relay',
  tagline: 'Harbor 镜像同步控制面：路由、分发、回调与通知',
  favicon: 'img/logo.svg',

  url: 'https://docs.example.com:9443',
  baseUrl: '/',

  organizationName: 'yuanyp8',
  projectName: 'harbor-relay',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'zh-Hans',
    locales: ['zh-Hans'],
  },

  themes: ['@docusaurus/theme-mermaid'],
  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  presets: [
    [
      'classic',
      {
        docs: {
          path: '../docs',
          routeBasePath: 'docs',
          sidebarPath: './sidebars.ts',
          showLastUpdateTime: true,
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      },
    ],
  ],

  themeConfig: {
    image: 'img/social-card.svg',
    navbar: {
      title: 'Harbor Relay',
      logo: {
        alt: 'Harbor Relay',
        src: 'img/logo.svg',
      },
      items: [
        {to: '/docs/intro', label: '文档', position: 'left'},
        {to: '/docs/03-ops-guide', label: '部署', position: 'left'},
        {to: '/docs/06-api-reference', label: 'API', position: 'left'},
        {
          href: 'https://github.com/yuanyp8/harbor-relay',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: '概览', to: '/docs/intro'},
            {label: '系统架构', to: '/docs/01-system-overview'},
            {label: '运维部署', to: '/docs/03-ops-guide'},
          ],
        },
        {
          title: 'Runbook',
          items: [
            {label: '全流程示例', to: '/docs/05-full-example'},
            {label: '通知与回调', to: '/docs/04-notification-and-callback'},
            {label: '排障手册', to: '/docs/07-troubleshooting'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: 'https://github.com/yuanyp8/harbor-relay'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Harbor Relay`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  },
};

export default config;
