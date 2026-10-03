FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
# CONTROL_API_URL/ADMIN_TOKEN are read at request time (server components,
# server actions), not baked in at build time, so no build-arg is needed
# here despite this being a "production build" step.
RUN npm run build

# Next's standalone output already includes a minimal server and only the
# node_modules it actually traced as used — copy just that, not the full
# node_modules from the build stage.
FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
EXPOSE 3000
CMD ["node", "server.js"]
