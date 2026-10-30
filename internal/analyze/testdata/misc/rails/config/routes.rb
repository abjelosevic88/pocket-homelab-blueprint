Rails.application.routes.draw do
  root "home#index"
  get "/about", to: "pages#about"
  namespace :api do
    resources :users, only: [:index, :show] do
      resources :posts, only: [:index]
    end
  end
end
