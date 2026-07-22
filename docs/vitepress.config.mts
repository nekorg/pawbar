import {
  defineConfig,
} from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: "pawbar",
  description: "Kat vibes for your desktop",
  srcExclude: ['**/README.md'],
  cleanUrls: true,
  head: [
    [
      'link',
      { rel: 'icon', type: 'image/svg+xml', href: '/pawbar.svg' }
    ],
    [
      'link',
      {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Karla:wght@400;500;700&family=Source+Code+Pro:wght@400;500;700&display=swap'
      }
    ],
    [
      // Symbols-only Nerd Font, so module icons (U+E000..U+F8FF and
      // plane-15 PUA) render without requiring a locally patched font.
      'link',
      {
        rel: 'stylesheet',
        href: 'https://www.nerdfonts.com/assets/css/webfont.css'
      }
    ]
  ],
  themeConfig: {
    logo: '/pawbar.svg',
    outline: [2, 3],
    search: { provider: 'local' },
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: 'Docs', link: '/docs/getting-started', activeMatch: "/docs/" },
    ],

    sidebar: [
      {
        text: 'Getting Started',
        link: '/docs/getting-started',
      },
      {
        text: 'Configuration',
        link: '/docs/configuration',
      },
      {
        text: 'Modules',
        collapsed: false,
        base: '/docs/modules',
        link: '/',
        items: [
          { text: 'Backlight', link: '#backlight' }, 
          { text: 'Battery', link: '#battery' }, 
          { text: 'Bluetooth', link: '#bluetooth' }, 
          { text: 'Clock', link: '#clock' }, 
          { text: 'CPU', link: '#cpu' }, 
          { text: 'Custom', link: '#custom' }, 
          { text: 'Disk', link: '#disk' }, 
          { text: 'Idle Inhibitor', link: '#idleInhibitor' }, 
          { text: 'Locale', link: '#locale' }, 
          { text: 'Mpris', link: '#mpris' }, 
          { text: 'RAM', link: '#ram' }, 
          { text: 'Window Title', link: '#title' }, 
          { text: 'Tray', link: '#tray' }, 
          { text: 'Volume', link: '#volume' }, 
          { text: 'Wi-Fi', link: '#wifi' }, 
          { text: 'Workspace', link: '#ws' }, 
        ]
      }
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/codelif/pawbar' }
    ],

    editLink: {
      pattern: 'https://github.com/codelif/pawbar/edit/main/docs/:path',
      text: 'Edit this page on Github'
    },

    footer: {
      message: 'Released under the BSD-3-Clause License.',
      copyright: 'Copyright © 2025 Harsh Sharma'
    }
  }
})



