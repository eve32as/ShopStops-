import type { ExpoConfig } from "expo/config";
import staticConfig from "./app.json";

const config = staticConfig.expo as ExpoConfig;

export default (): ExpoConfig => ({
  ...config,
  plugins: [
    ...(config.plugins ?? []),
    [
      "react-native-maps",
      {
        androidGoogleMapsApiKey: process.env.GOOGLE_MAPS_API_KEY,
      },
    ],
  ],
});
