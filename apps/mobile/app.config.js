const staticConfig = require("./app.json").expo;
const googleMapsApiKey = process.env.EXPO_PUBLIC_GOOGLE_MAPS_API_KEY?.trim();

module.exports = {
  ...staticConfig,
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
