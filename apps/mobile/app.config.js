const staticConfig = require("./app.json").expo;
const googleMapsApiKey = process.env.EXPO_PUBLIC_GOOGLE_MAPS_API_KEY?.trim();
const easProjectId = process.env.EAS_PROJECT_ID?.trim();

module.exports = {
  ...staticConfig,
  ...(easProjectId
    ? {
        extra: {
          ...staticConfig.extra,
          eas: { ...staticConfig.extra?.eas, projectId: easProjectId },
        },
      }
    : {}),
  android: {
    ...staticConfig.android,
    ...(googleMapsApiKey
      ? {
          config: {
            ...staticConfig.android?.config,
            googleMaps: { apiKey: googleMapsApiKey },
          },
        }
      : {}),
  },
};
