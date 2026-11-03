/** @type {import('next').NextConfig} */
module.exports = {
    output: 'export',
    basePath: '/poker',
    reactStrictMode: true,
    turbopack: {
        root: __dirname,
    },
    images: {
        unoptimized: true,
    },
};
