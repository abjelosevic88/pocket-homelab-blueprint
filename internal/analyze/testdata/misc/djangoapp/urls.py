from django.urls import include, path, re_path

from . import views

urlpatterns = [
    path("articles/<int:year>/", views.year_archive),
    re_path(r"^posts/(?P<slug>[\w-]+)/$", views.post),
    path("api/", include("djangoapp.api_urls")),
]
