export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'scope-enum': [
      2,
      'always',
      ['config', 'rules', 'store', 'exporter', 'broker', 'app', 'cmd', 'docker', 'ci', 'docs', 'deps', 'repo', 'feature'],
    ],
    'header-max-length': [2, 'always', 72],
  },
}
