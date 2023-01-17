setup:
	npm install json-to-env -g

from-github:
	cd functions/from-github && json-to-env ./env.json ./env.sh
	cd functions/from-github && source ./env.sh && rm ./env.sh && go run *.go

from-discogs:
	cd functions/from-discogs && json-to-env ./env.json ./env.sh
	cd functions/from-discogs && source ./env.sh && rm ./env.sh && go run *.go

from-youtube:
	cd functions/from-youtube && json-to-env ./env.json ./env.sh
	cd functions/from-youtube && source ./env.sh && rm ./env.sh && python3 main.py

to-spotify:
	cd functions/to-spotify && source ./env.sh && python3 main.py

to-www:
	cd functions/to-www && json-to-env ./env.json ./env.sh
	cd functions/to-www && source ./env.sh && rm ./env.sh && go run *.go

sort-playlists:
	cd functions/sort-playlists && json-to-env ./env.json ./env.sh
	cd functions/sort-playlists && source ./env.sh && rm ./env.sh && go run *.go

deploy-from-discogs:
	apex build from-discogs >/dev/null
	apex deploy from-discogs --region eu-west-1 -ldebug --env-file ./functions/from-discogs/env.json

deploy-from-github:
	apex build from-github >/dev/null
	apex deploy from-github --region eu-west-1 -ldebug --env-file ./functions/from-github/env.json

deploy-to-www:
	apex build to-www >/dev/null
	apex deploy to-www --region eu-west-1 -ldebug --env-file ./functions/to-www/env.json

deploy-from-youtube:
	apex build from-youtube >/dev/null
	apex deploy from-youtube --region eu-west-1 -ldebug --env-file ./functions/from-youtube/env.json

deploy-sort-playlists:
	apex build sort-playlists >/dev/null
	apex deploy sort-playlists --region eu-west-1 -ldebug --env-file ./functions/sort-playlists/env.json

setup-to-spotify:
	aws ecr create-repository --repository-name to-spotify --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1

deploy-to-spotify:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t to-spotify functions/to-spotify
	docker tag to-spotify:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-spotify:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-spotify:latest
	sh ./utils/set-ecr.sh to-spotify ${AWS_ACCOUNT_ID}
